import { execFile, type ExecFileOptionsWithStringEncoding } from 'child_process';
import { promises as fs, readFileSync, statSync } from 'fs';
import * as os from 'os';
import * as path from 'path';
import { TextDecoder, promisify } from 'util';
import * as vscode from 'vscode';
import YAML, { LineCounter, Pair, visit } from 'yaml';

const YAML_EXTENSION_ID = 'redhat.vscode-yaml';
const SCHEMA_SCHEME = 'dalecspec';
const DEBUG_TYPE = 'dalec-buildx';
const DEBUG_COMMAND = 'dalec.debugCurrentSpec';
const BUILD_COMMAND = 'dalec.buildCurrentSpec';
const SYNTAX_REGEX = /^#\s*(?:syntax|sytnax)\s*=\s*(?<image>ghcr\.io\/(?:project-dalec|azure)\/dalec\/frontend:[^\s#]+|[^\s#]*dalec[^\s#]*)/i;
const FALLBACK_SCHEMA_RELATIVE_PATH = ['schemas', 'spec.schema.json'];
const FRONTEND_TARGET_CACHE_TTL_MS = 5 * 60 * 1000;
const execFileAsync = promisify(execFile);
const frontendTargetCache = new Map<string, FrontendTargetCacheEntry>();
const emptyContextDirPath = path.join(os.tmpdir(), 'dalec-empty-context');
let emptyContextDirReady: Promise<string> | undefined;
const contextSelectionCache = new Map<string, ContextSelection>();
const argsSelectionCache = new Map<string, ArgsSelection>();
let dalecOutputChannel: vscode.OutputChannel | undefined;
let workspaceRoot: string | undefined;

export async function activate(context: vscode.ExtensionContext) {
  const tracker = new DalecDocumentTracker();
  context.subscriptions.push(tracker);
  const lastAction = new LastDalecActionState();
  dalecOutputChannel = vscode.window.createOutputChannel('Dalec Spec');
  context.subscriptions.push(dalecOutputChannel);

  workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

  const schemaProvider = new DalecSchemaProvider(context, tracker);
  await schemaProvider.initialize();
  context.subscriptions.push(schemaProvider);

  const codeLensProvider = new DalecCodeLensProvider(tracker, lastAction);
  context.subscriptions.push(
    vscode.languages.registerCodeLensProvider([{ language: 'yaml' }, { language: 'yml' }], codeLensProvider),
    codeLensProvider,
  );

  const debugProvider = new DalecDebugConfigurationProvider(tracker);
  context.subscriptions.push(
    vscode.debug.registerDebugConfigurationProvider(DEBUG_TYPE, debugProvider),
    vscode.debug.registerDebugAdapterDescriptorFactory(
      DEBUG_TYPE,
      new DalecDebugAdapterDescriptorFactory(),
    ),
    vscode.debug.registerDebugAdapterTrackerFactory(
      DEBUG_TYPE,
      new DalecDebugAdapterTrackerFactory(),
    ),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand(DEBUG_COMMAND, (uri?: vscode.Uri) => {
      void runDebugCommand(uri, tracker, lastAction);
    }),
    vscode.commands.registerCommand(BUILD_COMMAND, (uri?: vscode.Uri) =>
      runBuildCommand(uri, tracker, lastAction),
    ),
    vscode.commands.registerCommand('dalec.rerunLastAction', () => rerunLastAction(tracker, lastAction)),
    vscode.commands.registerCommand('dalec.rerunLastActionBuild', () =>
      rerunLastAction(tracker, lastAction, 'build'),
    ),
    vscode.commands.registerCommand('dalec.rerunLastActionDebug', () =>
      rerunLastAction(tracker, lastAction, 'debug'),
    ),
  );
}

export function deactivate() {
  // no-op – disposables handle teardown.
}

class DalecDocumentTracker implements vscode.Disposable {
  private readonly tracked = new Map<string, DalecDocumentMetadata>();
  private readonly disposables: vscode.Disposable[] = [];
  private readonly changeEmitter = new vscode.EventEmitter<vscode.Uri>();
  readonly onDidChange = this.changeEmitter.event;

  constructor() {
    this.disposables.push(
      vscode.workspace.onDidOpenTextDocument((doc) => this.evaluate(doc)),
      vscode.workspace.onDidChangeTextDocument((event) => this.evaluate(event.document)),
      vscode.workspace.onDidCloseTextDocument((doc) => {
        const key = doc.uri.toString();
        if (this.tracked.delete(key)) {
          this.changeEmitter.fire(doc.uri);
        }
      }),
    );

    vscode.workspace.textDocuments.forEach((doc) => this.evaluate(doc));
  }

  dispose() {
    this.tracked.clear();
    this.disposables.forEach((disposable) => disposable.dispose());
    this.changeEmitter.dispose();
  }

  isDalecDocument(document: vscode.TextDocument): boolean {
    return this.tracked.has(document.uri.toString());
  }

  has(resource: string): boolean {
    return this.tracked.has(resource);
  }

  getMetadata(resource: vscode.TextDocument | string): DalecDocumentMetadata | undefined {
    const key = typeof resource === 'string' ? resource : resource.uri.toString();
    return this.tracked.get(key);
  }

  private evaluate(document: vscode.TextDocument) {
    if (!this.isYamlFile(document)) {
      this.delete(document.uri);
      return;
    }

    const firstLine = document.lineCount > 0 ? document.lineAt(0).text.trim() : '';
    if (!firstLine || !SYNTAX_REGEX.test(firstLine)) {
      this.delete(document.uri);
      return;
    }

    const metadata: DalecDocumentMetadata = this.buildMetadata(document);

    const key = document.uri.toString();
    clearCachedContextSelection(document.uri);
    clearCachedArgsSelection(document.uri);
    this.tracked.set(key, metadata);
    this.changeEmitter.fire(document.uri);
  }

  private delete(uri: vscode.Uri) {
    const key = uri.toString();
    if (this.tracked.delete(key)) {
      this.changeEmitter.fire(uri);
    }
    clearCachedContextSelection(uri);
    clearCachedArgsSelection(uri);
  }

  private buildMetadata(document: vscode.TextDocument): DalecDocumentMetadata {
    const parsed = parseDalecSpec(document.getText());
    if (!parsed) {
      return {
        targets: [],
        contexts: [],
        args: new Map<string, string | undefined>(),
      };
    }

    return {
      targets: extractTargetsFromSpec(parsed),
      contexts: extractContextNamesFromSpec(parsed),
      args: extractArgsFromSpec(parsed),
    };
  }

  private isYamlFile(document: vscode.TextDocument): boolean {
    const fileName = document.uri.fsPath.toLowerCase();
    return (
      (fileName.endsWith('.yml') || fileName.endsWith('.yaml')) &&
      (document.languageId === 'yaml' || document.languageId === 'yml')
    );
  }
}

class DalecSchemaProvider implements vscode.Disposable {
  private readonly fallbackSchemaUri: vscode.Uri;
  private yamlApi: YamlExtensionApi | undefined;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly tracker: DalecDocumentTracker,
  ) {
    this.fallbackSchemaUri = vscode.Uri.joinPath(
      context.extensionUri,
      ...FALLBACK_SCHEMA_RELATIVE_PATH,
    );
  }

  async initialize() {
    const yamlExtension = vscode.extensions.getExtension<YamlExtensionExports>(YAML_EXTENSION_ID);
    if (!yamlExtension) {
      void vscode.window.showWarningMessage(
        'Dalec spec schema support requires the Red Hat YAML extension (redhat.vscode-yaml).',
      );
      return;
    }

    this.yamlApi = await yamlExtension.activate();

    if (!this.yamlApi?.registerContributor) {
      void vscode.window.showWarningMessage(
        'Installed YAML extension does not expose schema APIs; Dalec schema validation is disabled.',
      );
      return;
    }

    const registered = this.yamlApi.registerContributor(
      SCHEMA_SCHEME,
      (resource) => this.onRequestSchema(resource),
      (uri) => this.onRequestSchemaContent(uri),
    );

    if (!registered) {
      void vscode.window.showWarningMessage(
        'Dalec spec schema contributor could not be registered; another schema provider may already exist.',
      );
    }
  }

  dispose() {
    this.disposables.forEach((disposable) => disposable.dispose());
  }

  private onRequestSchema(resource: string): string | undefined {
    if (!this.tracker.has(resource)) {
      return undefined;
    }

    const documentUri = vscode.Uri.parse(resource);
    const workspaceFolder = vscode.workspace.getWorkspaceFolder(documentUri);
    const authority = workspaceFolder
      ? encodeURIComponent(workspaceFolder.uri.toString())
      : 'global';

    return `${SCHEMA_SCHEME}://${authority}/dalec-jsonschema`;
  }

  private async onRequestSchemaContent(uri: string): Promise<string | undefined> {
    const parsed = vscode.Uri.parse(uri);
    const authority = parsed.authority && parsed.authority !== 'global' ? parsed.authority : '';
    const workspaceUri = authority ? vscode.Uri.parse(decodeURIComponent(authority)) : undefined;

    const schemaContent = await this.readSchema(workspaceUri);
    return schemaContent;
  }

  private async readSchema(workspaceUri?: vscode.Uri): Promise<string | undefined> {
    if (workspaceUri) {
      const docPath = vscode.Uri.joinPath(workspaceUri, 'docs', 'spec.schema.json');
      try {
        const content = await vscode.workspace.fs.readFile(docPath);
        return new TextDecoder().decode(content);
      } catch {
        // Fall back below.
      }
    }

    try {
      const content = await vscode.workspace.fs.readFile(this.fallbackSchemaUri);
      return new TextDecoder().decode(content);
    } catch (error) {
      void vscode.window.showErrorMessage(
        `Unable to load Dalec spec schema (${this.fallbackSchemaUri.fsPath}): ${error}`,
      );
      return undefined;
    }
  }
}

class DalecCodeLensProvider implements vscode.CodeLensProvider, vscode.Disposable {
  private readonly emitter = new vscode.EventEmitter<void>();
  readonly onDidChangeCodeLenses = this.emitter.event;
  private readonly trackerSubscription: vscode.Disposable;

  constructor(private readonly tracker: DalecDocumentTracker, private readonly lastAction: LastDalecActionState) {
    this.trackerSubscription = this.tracker.onDidChange(() => this.emitter.fire());
  }

  provideCodeLenses(document: vscode.TextDocument): vscode.CodeLens[] | undefined {
    if (!this.tracker.isDalecDocument(document)) {
      return undefined;
    }

    const range = new vscode.Range(0, 0, 0, 0);
    const lenses: vscode.CodeLens[] = [
      new vscode.CodeLens(range, {
        command: BUILD_COMMAND,
        title: 'Dalec: Build',
        arguments: [document.uri],
      }),
    ];

    lenses.unshift(
      new vscode.CodeLens(range, {
        command: DEBUG_COMMAND,
        title: 'Dalec: Debug',
        arguments: [document.uri],
      }),
    );

    const last = this.lastAction.get();
    if (last && last.specUri.toString() === document.uri.toString()) {
      lenses.push(
        new vscode.CodeLens(range, {
          command: 'dalec.rerunLastActionDebug',
          title: `Dalec: Debug (${last.target})`,
        }),
      );
      lenses.push(
        new vscode.CodeLens(range, {
          command: 'dalec.rerunLastActionBuild',
          title: `Dalec: Build (${last.target})`,
        }),
      );
    }

    return lenses;
  }

  dispose() {
    this.trackerSubscription.dispose();
    this.emitter.dispose();
  }
}

class DalecDebugConfigurationProvider implements vscode.DebugConfigurationProvider {
  constructor(private readonly tracker: DalecDocumentTracker) {}

  async resolveDebugConfiguration(
    folder: vscode.WorkspaceFolder | undefined,
    config: vscode.DebugConfiguration,
  ): Promise<vscode.DebugConfiguration | null | undefined> {
    config.type = DEBUG_TYPE;
    config.request = config.request ?? 'launch';

    if (config.request !== 'launch') {
      void vscode.window.showErrorMessage('Dalec Buildx debugger only supports launch requests.');
      return null;
    }

    if (!config.target || typeof config.target !== 'string') {
      void vscode.window.showErrorMessage('A Dalec target name is required (debug configuration "target").');
      return null;
    }

    let specFile = typeof config.specFile === 'string' ? config.specFile.trim() : '';
    if (!specFile) {
      const doc = vscode.window.activeTextEditor?.document;
      if (doc && this.tracker.isDalecDocument(doc)) {
        specFile = doc.uri.fsPath;
      }
    }

    if (!specFile) {
      void vscode.window.showErrorMessage(
        'No Dalec spec file set. Provide "specFile" or focus a Dalec YAML document.',
      );
      return null;
    }

    config.specFile = specFile;

    return config;
  }

  async resolveDebugConfigurationWithSubstitutedVariables(
    folder: vscode.WorkspaceFolder | undefined,
    config: vscode.DebugConfiguration,
  ): Promise<vscode.DebugConfiguration | null | undefined> {
    if (!config.target || typeof config.target !== 'string') {
      void vscode.window.showErrorMessage('A Dalec target name is required (debug configuration "target").');
      return null;
    }

    const specFile = typeof config.specFile === 'string' ? config.specFile.trim() : '';
    if (!specFile) {
      void vscode.window.showErrorMessage('Dalec spec file could not be resolved.');
      return null;
    }

    const resolvedSpec = this.resolvePath(specFile, folder);
    // const resolvedSpec = specFile;
    try {
      await vscode.workspace.fs.stat(vscode.Uri.file(resolvedSpec));
    } catch {
      void vscode.window.showErrorMessage(`Dalec spec file not found: ${resolvedSpec}`);
      return null;
    }

    config.specFile = resolvedSpec;

    if (typeof config.cwd === 'string' && config.cwd.trim()) {
      config.cwd = this.resolvePath(config.cwd, folder);
    }

    if (config.buildArgs && typeof config.buildArgs !== 'object') {
      void vscode.window.showWarningMessage('Ignoring buildArgs – value must be an object map.');
      delete config.buildArgs;
    }

    const document = await vscode.workspace.openTextDocument(resolvedSpec);
    if (!this.tracker.isDalecDocument(document)) {
      void vscode.window.showErrorMessage('Selected file is not recognized as a Dalec spec.');
      return null;
    }

    if (!config.dalecContextResolved) {
      const selection = await collectContextSelection(document, this.tracker);
      if (!selection) {
        return undefined;
      }
      config.context = selection.defaultContextPath;
      config.buildContexts = recordFromMap(selection.additionalContexts);
      config.dalecContextResolved = true;
    }

    if (!config.context) {
      config.context = await getEmptyContextDir();
    }

    const workspaceForSpec = vscode.workspace.getWorkspaceFolder(vscode.Uri.file(resolvedSpec)) ?? folder;
    config.context = this.resolvePath(config.context, workspaceForSpec);

    if (config.buildContexts && typeof config.buildContexts === 'object') {
      const resolved: Record<string, string> = {};
      const entries = Object.entries(config.buildContexts as Record<string, string>);
      for (const [name, ctxPath] of entries) {
        resolved[name] = this.resolvePath(ctxPath, workspaceForSpec);
      }
      config.buildContexts = resolved;
    }

    return config;
  }

  private containsVariableReference(value: string): boolean {
    return value.includes('${');
  }

  private resolvePath(input: string, folder: vscode.WorkspaceFolder | undefined): string {
    if (path.isAbsolute(input)) {
      return input;
    }

    if (folder) {
      return path.join(folder.uri.fsPath, input);
    }

    const workspaceFolder = vscode.workspace.workspaceFolders?.[0];
    if (workspaceFolder) {
      return path.join(workspaceFolder.uri.fsPath, input);
    }

    return path.resolve(input);
  }
}

class DalecDebugAdapterDescriptorFactory implements vscode.DebugAdapterDescriptorFactory {
  createDebugAdapterDescriptor(
    session: vscode.DebugSession,
  ): vscode.ProviderResult<vscode.DebugAdapterDescriptor> {
    const config = session.configuration as DalecDebugConfiguration;
    const dockerCommand = createDockerBuildxCommand({
      mode: 'dap',
      target: config.target,
      specFile: config.specFile,
      context: config.context,
      buildArgs: mapFromRecord(config.buildArgs),
      buildContexts: mapFromRecord(config.buildContexts),
      noCache: false,
    });
    logDockerCommand('Debug command', dockerCommand, { toDebugConsole: true });

    const options: vscode.DebugAdapterExecutableOptions = {
      cwd: config.cwd ?? config.workspaceFolder ?? getWorkspaceRootForPath(config.specFile) ?? process.cwd(),
      env: {
        ...process.env,
        BUILDX_EXPERIMENTAL: '1',
      },
    };

    return new vscode.DebugAdapterExecutable(dockerCommand.binary, dockerCommand.args, options);
  }
}

class DalecDebugAdapterTrackerFactory implements vscode.DebugAdapterTrackerFactory {
  createDebugAdapterTracker(_session: vscode.DebugSession): vscode.DebugAdapterTracker {
    return {
      onWillReceiveMessage: (message) => {
        rewriteSourcePathsForBreakpoints(message);
        logDapTraffic('client->server', message);
      },
      onDidSendMessage: (message) => logDapTraffic('server->client', message),
      onError: (error) => logDapTraffic('error', error),
      onExit: (code, signal) => logDapTraffic('exit', { code, signal }),
    };
  }
}

interface DalecDebugConfiguration extends vscode.DebugConfiguration {
  target: string;
  specFile: string;
  context: string;
  buildArgs?: Record<string, string>;
  buildContexts?: Record<string, string>;
  dalecContextResolved?: boolean;
  workspaceFolder?: string;
  cwd?: string;
}

interface BuildTargetInfo {
  name: string;
  description?: string;
}

interface DalecDocumentMetadata {
  targets: string[];
  contexts: string[];
  args: Map<string, string | undefined>;
}

type DockerCommandMode = 'build' | 'dap';

interface DockerCommandInputs {
  mode: DockerCommandMode;
  target: string;
  specFile: string;
  context: string;
  buildArgs?: Map<string, string>;
  buildContexts?: Map<string, string>;
  noCache?: boolean;
}

interface DockerCommand {
  binary: string;
  args: string[];
}

type DalecSpecDocument = Record<string, unknown>;

function parseDalecSpec(text: string): DalecSpecDocument | undefined {
  if (!text.trim()) {
    return undefined;
  }

  try {
    const parsed = YAML.parse(text);
    if (isRecordLike(parsed)) {
      return parsed;
    }
  } catch {
    // Ignore parse errors; fall back to empty metadata.
  }
  return undefined;
}

function extractTargetsFromSpec(spec: DalecSpecDocument): string[] {
  const rawTargets = spec.targets;
  if (!isRecordLike(rawTargets)) {
    return [];
  }
  return Object.keys(rawTargets);
}

function extractContextNamesFromSpec(spec: DalecSpecDocument): string[] {
  const contexts = new Set<string>();
  collectContextNames(spec, contexts);
  return [...contexts];
}

function collectContextNames(value: unknown, results: Set<string>) {
  if (Array.isArray(value)) {
    for (const entry of value) {
      collectContextNames(entry, results);
    }
    return;
  }
  if (!isRecordLike(value)) {
    return;
  }

  if (Object.prototype.hasOwnProperty.call(value, 'context')) {
    const name = getContextName(value.context);
    if (name) {
      results.add(name);
    }
  }

  for (const child of Object.values(value)) {
    collectContextNames(child, results);
  }
}

function getContextName(value: unknown): string | undefined {
  if (value === null || value === undefined) {
    return 'context';
  }
  if (typeof value === 'string') {
    return sanitizeContextName(value);
  }
  if (isRecordLike(value)) {
    const rawName = value.name;
    if (typeof rawName === 'string') {
      return sanitizeContextName(rawName);
    }
    return 'context';
  }
  return undefined;
}

function extractArgsFromSpec(spec: DalecSpecDocument): Map<string, string | undefined> {
  const result = new Map<string, string | undefined>();
  const rawArgs = spec.args;
  if (!isRecordLike(rawArgs)) {
    return result;
  }

  for (const [key, value] of Object.entries(rawArgs)) {
    if (value === null || value === undefined) {
      result.set(key, undefined);
      continue;
    }
    if (typeof value === 'string') {
      result.set(key, value);
      continue;
    }
    result.set(key, String(value));
  }
  return result;
}

function isRecordLike(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function createDockerBuildxCommand(inputs: DockerCommandInputs): DockerCommand {
  const buildxSetting = vscode.workspace.getConfiguration('dalec-spec').get('buildxCommand', 'docker buildx').trim();
  const parts = buildxSetting.split(/\s+/);
  const binary = parts.shift() || 'docker';
  const args = parts;
  if (inputs.mode === 'dap') {
    args.push('dap', 'build');
  } else {
    args.push('build');
  }
  args.push('--target', inputs.target, '-f', getWorkspaceRelativeFsPath(inputs.specFile));
  if (inputs.buildArgs && inputs.buildArgs.size > 0) {
    args.push(...formatBuildArgs(inputs.buildArgs));
  }
  if (inputs.buildContexts && inputs.buildContexts.size > 0) {
    args.push(...buildContextArgs(inputs.buildContexts));
  }
  if (inputs.noCache) {
    args.push('--no-cache');
  }
  const contextPathArg = isRemoteContextReference(inputs.context)
    ? inputs.context
    : getWorkspaceRelativeFsPath(inputs.context);
  args.push(contextPathArg);
  return { binary, args };
}

function formatDockerCommand(command: DockerCommand): string {
  return [command.binary, ...command.args].map(quote).join(' ');
}

function logDockerCommand(scope: string, command: DockerCommand, options?: { toDebugConsole?: boolean }): string {
  const formatted = formatDockerCommand(command);
  const line = `[Dalec] ${scope}: ${formatted}`;
  dalecOutputChannel?.appendLine(line);
  if (options?.toDebugConsole) {
    vscode.debug.activeDebugConsole?.appendLine(line);
  }
  return formatted;
}

function mapFromRecord(record?: Record<string, string>): Map<string, string> {
  if (!record) {
    return new Map();
  }
  return new Map(Object.entries(record));
}

function logDapTraffic(direction: string, payload: unknown) {
  const serialized = safeSerialize(payload);
  dalecOutputChannel?.appendLine(`[Dalec][DAP][${direction}] ${serialized}`);
}

function safeSerialize(payload: unknown): string {
  try {
    return typeof payload === 'string' ? payload : JSON.stringify(payload, undefined, 2);
  } catch {
    return String(payload);
  }
}

function rewriteSourcePathsForBreakpoints(message: unknown) {
  if (!message || typeof message !== 'object') {
    return;
  }

  const isSetBreakpoints = (message as { command?: string }).command === 'setBreakpoints';
  const isLoadedSource = (message as { event?: string }).event === 'loadedSource';

  if (!isSetBreakpoints && !isLoadedSource) {
    return;
  }

  if (isLoadedSource) {
    const source = (message as { body?: { source?: { name?: string; path?: string } } }).body?.source;
    const sourcePath = source?.path;
    if (sourcePath) {
      lastLoadedSources.set(source.name ?? sourcePath, sourcePath);
    }
    return;
  }

  const args = (message as { arguments?: Record<string, unknown> }).arguments;
  if (!args || typeof args !== 'object') {
    return;
  }

  const source = args.source as { name?: string; path?: string } | undefined;
  if (!source) {
    return;
  }

  const key = source.name ?? source.path;
  const preferredPath = key ? lastLoadedSources.get(key) : undefined;
  if (preferredPath) {
    source.path = preferredPath;
  }

  const fileContent = source.path ? getFileContent(source.path) : undefined;
  const lines = fileContent?.lines;
  const mappingRanges =
    source.path && fileContent ? getYamlMappingRanges(source.path, fileContent) : undefined;

  let breakpoints = Array.isArray(args.breakpoints)
    ? (args.breakpoints as { line?: number; column?: number; }[])
    : undefined;

  if (!breakpoints && Array.isArray(args.lines)) {
    breakpoints = (args.lines as number[]).map((line) => ({ line }));
    args.breakpoints = breakpoints;
  }

  if (!Array.isArray(breakpoints)) {
    return;
  }

  for (const bp of breakpoints) {
    if (!bp || typeof bp.line !== 'number' || bp.line <= 0) {
      continue;
    }
    const bpLine = bp.line;
    const lineText = lines?.[bpLine - 1];
    const colonIdx = lineText?.indexOf(':') ?? -1;
    const approxColumn = colonIdx >= 0 ? colonIdx + 1 : findFirstContentColumn(lineText ?? '');
    const matchingRanges = mappingRanges?.filter(
      (range) => range.startLine === bpLine,
    );
    if (matchingRanges && matchingRanges.length > 0) {
      matchingRanges.sort(
        (a, b) => scoreRange(a, bpLine, approxColumn) - scoreRange(b, bpLine, approxColumn),
      );
      const resolved = matchingRanges[0];
      const startColumn =
        bpLine === resolved.startLine
          ? resolved.startColumn
          : Math.max(resolved.startColumn, approxColumn);
      bp.column = startColumn;
      continue;
    }

    const derivedRange = deriveRangeFromText(lines, bpLine);
    if (derivedRange) {
      bp.column = derivedRange.startColumn;
      continue;
    }

    const column = approxColumn;
    bp.column = column;
  }

  delete args.lines;
}

const lastLoadedSources = new Map<string, string>();
function scoreRange(range: MappingRange, line: number, approxColumn: number): number {
  if (range.startLine === line) {
    return Math.abs(range.startColumn - approxColumn);
  }
  return Number.MAX_SAFE_INTEGER;
}

function deriveRangeFromText(lines: string[] | undefined, line: number): MappingRange | undefined {
  if (!lines || line <= 0 || line > lines.length) {
    return undefined;
  }

  const current = lines[line - 1];
  const colonIdx = current.indexOf(':');
  if (colonIdx >= 0) {
    return {
      startLine: line,
      startColumn: indexToColumn(colonIdx),
    };
  }

  return findParentMapping(lines, line);
}

function findParentMapping(lines: string[], line: number): MappingRange | undefined {
  const current = lines[line - 1];
  const currentIndent = leadingWhitespace(current);
  for (let idx = line - 1; idx >= 1; idx--) {
    const candidate = lines[idx - 1];
    if (!candidate.trim()) {
      continue;
    }
    const colonIdx = candidate.indexOf(':');
    if (colonIdx === -1) {
      continue;
    }
    const candidateIndent = leadingWhitespace(candidate);
    if (currentIndent > candidateIndent) {
      return {
        startLine: idx,
        startColumn: indexToColumn(colonIdx),
      };
    }
  }
  return undefined;
}

function leadingWhitespace(value: string): number {
  const match = value.match(/^\s*/);
  return match ? match[0].length : 0;
}

interface FileContent {
  lines: string[];
  text: string;
  cacheKey: string;
}

function getFileContent(filePath: string): FileContent | undefined {
  const openDocument = vscode.workspace.textDocuments.find(
    (doc) => doc.uri.scheme === 'file' && doc.uri.fsPath === filePath,
  );
  if (openDocument) {
    const text = openDocument.getText();
    return {
      lines: text.split(/\r?\n/),
      text,
      cacheKey: `doc:${openDocument.version}`,
    };
  }

  try {
    const text = readFileSync(filePath, 'utf8');
    const stat = statSync(filePath);
    return {
      lines: text.split(/\r?\n/),
      text,
      cacheKey: `fs:${stat.mtimeMs}:${stat.size}`,
    };
  } catch {
    return undefined;
  }
}

interface MappingRange {
  startLine: number;
  startColumn: number;
}

const yamlRangeCache = new Map<string, MappingRange[]>();

function getYamlMappingRanges(filePath: string, content: FileContent): MappingRange[] | undefined {
  const cacheKey = `${filePath}:${content.cacheKey}`;
  const cached = yamlRangeCache.get(cacheKey);
  if (cached) {
    return cached;
  }

  try {
    const lineCounter = new LineCounter();
    const doc = YAML.parseDocument(content.text, {
      keepSourceTokens: true,
      lineCounter,
    });

    const ranges: MappingRange[] = [];
    visit(doc, {
      Pair(_key, pair: Pair) {
        if (!pair?.srcToken) {
          return;
        }
        const colonToken = pair.srcToken.sep?.find(
          (token) => token.type === 'map-value-ind' && typeof token.offset === 'number',
        );
        if (!colonToken) {
          return;
        }
        const start = lineCounter.linePos(colonToken.offset);
        const resolved = resolveNodeEnd(
          pair.value,
          pair.srcToken.value,
          lineCounter,
          content.lines,
          start.line,
        );
        if (!resolved) {
          return;
        }
        ranges.push({
          startLine: start.line,
          startColumn: indexToColumn(start.col),
        });
      },
    });

    yamlRangeCache.set(cacheKey, ranges);
    return ranges;
  } catch {
    return undefined;
  }
}

type LinePosition = { line: number; col: number };

function resolveNodeEnd(
  node: unknown,
  tokenValue: { offset?: number; source?: string; type?: string } | undefined,
  lineCounter: LineCounter,
  lines: string[],
  startLine: number,
): LinePosition | undefined {
  const range = (node as { range?: [number, number] })?.range;
  if (Array.isArray(range) && typeof range[1] === 'number') {
    return lineCounter.linePos(range[1]);
  }

  if (
    tokenValue &&
    typeof tokenValue.offset === 'number' &&
    typeof tokenValue.source === 'string'
  ) {
    return lineCounter.linePos(tokenValue.offset + tokenValue.source.length);
  }

  return undefined;
}

function findFirstContentColumn(line: string): number {
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (ch !== ' ' && ch !== '\t') {
      return i + 1;
    }
  }
  return 1;
}

function indexToColumn(index: number): number {
  // Convert 0-based index to 1-based column number
  return index + 1;
}

async function runDebugCommand(
  uri: vscode.Uri | undefined,
  tracker: DalecDocumentTracker,
  lastAction: LastDalecActionState,
) {

  const document = await resolveDalecDocument(uri, tracker);
  if (!document) {
    return;
  }

  const target = await pickTarget(document, tracker, 'Select a Dalec target to debug');
  if (!target) {
    return;
  }

  const contextSelection = await collectContextSelection(document, tracker);
  if (!contextSelection) {
    return;
  }

  const argsSelection = await collectArgsSelection(document, tracker);
  if (!argsSelection) {
    return;
  }

  const folder = vscode.workspace.getWorkspaceFolder(document.uri);
  const workspacePath = getWorkspaceRootForUri(document.uri);
  const configuration: DalecDebugConfiguration = {
    type: DEBUG_TYPE,
    name: `Dalec: Debug (${target})`,
    request: 'launch',
    target,
    specFile: getWorkspaceRelativeSpecPath(document.uri),
    context: contextSelection.defaultContextPath,
    buildContexts: recordFromMap(contextSelection.additionalContexts),
    dalecContextResolved: true,
    buildArgs: recordFromMap(argsSelection.values),
    workspaceFolder: workspacePath,
    cwd: workspacePath,
  };
  lastAction.record({
    type: 'debug',
    target,
    specUri: document.uri,
    contexts: contextSelection,
    args: argsSelection,
  });

  await vscode.debug.startDebugging(folder, configuration);
}

async function runBuildCommand(
  uri: vscode.Uri | undefined,
  tracker: DalecDocumentTracker,
  lastAction: LastDalecActionState,
) {
  const document = await resolveDalecDocument(uri, tracker);
  if (!document) {
    return;
  }

  const target = await pickTarget(document, tracker, 'Select a Dalec target to build');
  if (!target) {
    return;
  }

  const contextSelection = await collectContextSelection(document, tracker);
  if (!contextSelection) {
    return;
  }

  const argsSelection = await collectArgsSelection(document, tracker);
  if (!argsSelection) {
    return;
  }

  const dockerCommand = createDockerBuildxCommand({
    mode: 'build',
    target,
    specFile: document.uri.fsPath,
    context: contextSelection.defaultContextPath,
    buildArgs: argsSelection.values,
    buildContexts: contextSelection.additionalContexts,
  });
  const formattedCommand = logDockerCommand('Build command', dockerCommand);
  const terminal = vscode.window.createTerminal({
    name: `Dalec Build (${target})`,
    cwd: getWorkspaceRootForUri(document.uri),
    env: {
      ...process.env,
      BUILDX_EXPERIMENTAL: '1',
    },
  });
  terminal.show();
  terminal.sendText(`${getTerminalCommentPrefix()} Dalec command: ${formattedCommand}`);
  terminal.sendText(formattedCommand);

  lastAction.record({
    type: 'build',
    target,
    specUri: document.uri,
    contexts: contextSelection,
    args: argsSelection,
  });
}

async function resolveDalecDocument(
  uri: vscode.Uri | undefined,
  tracker: DalecDocumentTracker,
): Promise<vscode.TextDocument | undefined> {
  if (uri) {
    const doc = await vscode.workspace.openTextDocument(uri);
    if (tracker.isDalecDocument(doc)) {
      return doc;
    }
    void vscode.window.showErrorMessage('Selected file is not recognized as a Dalec spec.');
    return undefined;
  }

  const activeDoc = vscode.window.activeTextEditor?.document;
  if (activeDoc && tracker.isDalecDocument(activeDoc)) {
    return activeDoc;
  }

  void vscode.window.showErrorMessage('Open a Dalec spec (first line must start with #syntax=...) to continue.');
  return undefined;
}

async function pickTarget(
  document: vscode.TextDocument,
  tracker: DalecDocumentTracker,
  placeholder: string,
): Promise<string | undefined> {
  const targets = await getTargetsForDocument(document, tracker);

  if (targets.length === 0) {
    const manual = await vscode.window.showInputBox({
      prompt: 'No targets detected in this spec. Enter a target name to use.',
      placeHolder: 'target-name',
    });
    return manual?.trim() || undefined;
  }

  if (targets.length === 1) {
    return targets[0].name;
  }

  const scoped = groupTargets(targets);
  const scopes = [...scoped.keys()].sort((a, b) => {
    const aDebug = isDebugScope(a);
    const bDebug = isDebugScope(b);
    if (aDebug && !bDebug) {
      return 1;
    }
    if (!aDebug && bDebug) {
      return -1;
    }
    return a.localeCompare(b);
  });

  const scopeChoice = await vscode.window.showQuickPick(scopes, {
    placeHolder: 'Select target group',
  });
  if (!scopeChoice) {
    return undefined;
  }

  const scopeTargets = scoped.get(scopeChoice)!;
  scopeTargets.sort((a, b) => a.name.localeCompare(b.name));
  const targetChoice = await vscode.window.showQuickPick(
    scopeTargets.map((targetInfo) => ({
      label: targetInfo.name.slice(targetInfo.name.indexOf('/') + 1) || targetInfo.name,
      detail: targetInfo.description,
      target: targetInfo.name,
    })),
    {
      placeHolder: placeholder,
      matchOnDetail: true,
    },
  );

  return targetChoice?.target;
}

function quote(value: string): string {
  if (value.includes(' ')) {
    return `"${value.replace(/(["\\$`])/g, '\\$1')}"`;
  }
  return value;
}

function getSpecWorkspacePath(document: vscode.TextDocument): string {
  const folder = vscode.workspace.getWorkspaceFolder(document.uri);
  return folder?.uri.fsPath ?? path.dirname(document.uri.fsPath);
}

function getWorkspaceRelativeSpecPath(uri: vscode.Uri): string {
  return uri.fsPath;
}

function getWorkspaceRelativeFsPath(filePath: string): string {
  return filePath;
}

function getWorkspaceRootForPath(filePath?: string): string | undefined {
  if (filePath) {
    const uri = vscode.Uri.file(filePath);
    const folder = vscode.workspace.getWorkspaceFolder(uri);
    if (folder) {
      return folder.uri.fsPath;
    }
  }
  return workspaceRoot ?? vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
}

function getWorkspaceRootForUri(uri?: vscode.Uri): string | undefined {
  if (uri) {
    const folder = vscode.workspace.getWorkspaceFolder(uri);
    if (folder) {
      return folder.uri.fsPath;
    }
  }
  return workspaceRoot ?? vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
}

function buildContextArgs(contexts: Map<string, string>): string[] {
  const entries = [...contexts.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  const args: string[] = [];
  for (const [name, ctxPath] of entries) {
    const value = isRemoteContextReference(ctxPath)
      ? ctxPath
      : getWorkspaceRelativeFsPath(ctxPath);
    args.push('--build-context', `${name}=${value}`);
  }
  return args;
}

function formatBuildArgs(args: Map<string, string>): string[] {
  const entries = [...args.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  const flags: string[] = [];
  for (const [key, value] of entries) {
    flags.push('--build-arg', `${key}=${value}`);
  }
  return flags;
}

function sanitizeContextName(raw: string | undefined): string {
  if (!raw) {
    return 'context';
  }

  let cleaned = raw.trim();
  const commentIndex = cleaned.indexOf('#');
  if (commentIndex !== -1) {
    cleaned = cleaned.slice(0, commentIndex).trim();
  }
  cleaned = cleaned.replace(/[,}]+$/, '').trim();
  if (
    (cleaned.startsWith('"') && cleaned.endsWith('"')) ||
    (cleaned.startsWith("'") && cleaned.endsWith("'"))
  ) {
    cleaned = cleaned.slice(1, -1);
  }
  return cleaned || 'context';
}
async function getTargetsForDocument(
  document: vscode.TextDocument,
  tracker: DalecDocumentTracker,
): Promise<BuildTargetInfo[]> {
  const merged = new Map<string, BuildTargetInfo>();
  const trackedTargets = tracker.getMetadata(document)?.targets ?? [];
  for (const name of trackedTargets) {
    if (!merged.has(name)) {
      merged.set(name, { name });
    }
  }

  const frontendTargets = await getFrontendTargets(document);
  frontendTargets?.forEach((info) => merged.set(info.name, info));

  return [...merged.values()].sort((a, b) => a.name.localeCompare(b.name));
}

async function getFrontendTargets(document: vscode.TextDocument): Promise<BuildTargetInfo[] | undefined> {
  const key = document.uri.toString();
  const now = Date.now();
  const cached = frontendTargetCache.get(key);
  if (cached && now - cached.timestamp < FRONTEND_TARGET_CACHE_TTL_MS) {
    return cached.targets;
  }

  return vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Notification,
      title: 'Querying Dalec targets via docker buildx...',
      cancellable: false,
    },
    async () => {
      try {
        const contextPath = getSpecWorkspacePath(document);
        const args = [
          'buildx',
          'build',
          '--progress=quiet',
          '--call',
          'targets,format=json',
          '-f',
          document.uri.fsPath,
          contextPath,
        ];
        const execOptions: ExecFileOptionsWithStringEncoding = {
          cwd: contextPath,
          env: {
            ...process.env,
            BUILDX_EXPERIMENTAL: '1',
          },
          maxBuffer: 20 * 1024 * 1024,
          encoding: 'utf8',
        };
        const { stdout } = await execFileAsync('docker', args, execOptions);
        const parsed = parseTargetsFromOutput(stdout);
        if (parsed.length > 0) {
          frontendTargetCache.set(key, { targets: parsed, timestamp: Date.now() });
        }
        return parsed;
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        void vscode.window.showWarningMessage(`Failed to query Dalec targets: ${message}`);
        return cached?.targets;
      }
      },
    );
}

function parseTargetsFromOutput(output: string): BuildTargetInfo[] {
  const jsonTargets = parseTargetsFromJson(output);
  if (jsonTargets.length > 0) {
    return jsonTargets;
  }
  return parseTargetsFromText(output);
}

function parseTargetsFromJson(output: string): BuildTargetInfo[] {
  const trimmed = output.trim();
  if (!trimmed) {
    return [];
  }

  const jsonStart = trimmed.indexOf('{');
  if (jsonStart === -1) {
    return [];
  }

  try {
    const parsed = JSON.parse(trimmed.slice(jsonStart));
    if (!parsed || typeof parsed !== 'object' || !Array.isArray((parsed as { targets?: unknown }).targets)) {
      return [];
    }

    const targetsField = (parsed as { targets: unknown[] }).targets;
    const targets: BuildTargetInfo[] = [];
    for (const entry of targetsField) {
      if (!entry || typeof entry !== 'object') {
        continue;
      }
      const rawName = typeof (entry as { name?: unknown }).name === 'string'
        ? (entry as { name: string }).name.trim()
        : '';
      const name =
        rawName ||
        ((entry as { default?: unknown }).default ? '(default)' : '');
      if (!name) {
        continue;
      }
      const descriptionValue = (entry as { description?: unknown }).description;
      const description =
        typeof descriptionValue === 'string' && descriptionValue.trim()
          ? descriptionValue.trim()
          : undefined;
      targets.push({ name, description });
    }
    return targets;
  } catch {
    return [];
  }
}

function parseTargetsFromText(output: string): BuildTargetInfo[] {
  const targets: BuildTargetInfo[] = [];
  for (const line of output.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || /^target/i.test(trimmed) || trimmed.startsWith('=') || trimmed.startsWith('-')) {
      continue;
    }

    const match =
      trimmed.match(/^([A-Za-z0-9._/-]+)(?:\s+\(default\))?(?:\s{2,}(.*))?$/) ??
      trimmed.match(/^([A-Za-z0-9._/-]+)\s+(.*)$/);
    if (!match) {
      continue;
    }
    const name = match[1];
    const description = match[2]?.trim() || undefined;
    targets.push({ name, description });
  }
  return targets;
}

type YamlExtensionExports = {
  registerContributor?: (
    schema: string,
    requestSchema: (resource: string) => string | undefined,
    requestSchemaContent?: (uri: string) => Promise<string | undefined>,
  ) => boolean;
};

type YamlExtensionApi = YamlExtensionExports;

interface FrontendTargetCacheEntry {
  targets: BuildTargetInfo[];
  timestamp: number;
}

interface ContextSelection {
  defaultContextPath: string;
  additionalContexts: Map<string, string>;
}

interface ArgsSelection {
  values: Map<string, string>;
}

async function collectContextSelection(
  document: vscode.TextDocument,
  tracker: DalecDocumentTracker,
  cachedValue?: ContextSelection,
): Promise<ContextSelection | undefined> {
  const key = document.uri.toString();
  const metadata = tracker.getMetadata(document);
  const contextNames = new Set(metadata?.contexts ?? []);

  if (contextNames.size === 0) {
    const selection: ContextSelection = {
      defaultContextPath: await getEmptyContextDir(),
      additionalContexts: new Map(),
    };
    contextSelectionCache.set(key, selection);
    return selection;
  }

  const previousSelection = cachedValue ?? contextSelectionCache.get(key);
  const sortedNames = [...contextNames].sort();
  const selections = new Map<string, string>();
  let defaultPath = previousSelection?.defaultContextPath ?? (await getEmptyContextDir());

  for (const name of sortedNames) {
    const promptLabel = name === 'context' ? 'default build context' : `build context "${name}"`;
    const defaultValue =
      name === 'context'
        ? toInputValue(previousSelection?.defaultContextPath)
        : toInputValue(previousSelection?.additionalContexts.get(name));
    const value = await vscode.window.showInputBox({
      prompt: `Enter path for ${promptLabel}`,
      value: defaultValue,
    });
    if (value === undefined) {
      return undefined;
    }
    const resolvedPath = resolveContextReference(value.trim() || '.', document);
    if (name === 'context') {
      defaultPath = resolvedPath;
    } else {
      selections.set(name, resolvedPath);
    }
  }

  const selection: ContextSelection = {
    defaultContextPath: defaultPath,
    additionalContexts: selections,
  };
  contextSelectionCache.set(key, selection);
  return selection;
}

async function collectArgsSelection(
  document: vscode.TextDocument,
  tracker: DalecDocumentTracker,
  cachedValue?: ArgsSelection,
): Promise<ArgsSelection | undefined> {
  const metadata = tracker.getMetadata(document);
  const definedArgs = metadata?.args;

  if (!definedArgs || definedArgs.size === 0) {
    const emptySelection: ArgsSelection = { values: new Map() };
    argsSelectionCache.set(document.uri.toString(), emptySelection);
    return emptySelection;
  }

  const key = document.uri.toString();
  const previousSelection = cachedValue ?? argsSelectionCache.get(key);
  const result = new Map<string, string>();

  for (const [name, defaultValue] of definedArgs.entries()) {
    const value = await vscode.window.showInputBox({
      prompt: `Enter value for build argument "${name}"`,
      value: previousSelection?.values.get(name) ?? defaultValue ?? '',
      placeHolder: defaultValue ?? '',
    });
    if (value === undefined) {
      return undefined;
    }
    result.set(name, value);
  }

  const selection: ArgsSelection = { values: result };
  argsSelectionCache.set(key, selection);
  return selection;
}

function resolveContextReference(input: string, document: vscode.TextDocument): string {
  const trimmed = input.trim();
  if (!trimmed || trimmed === '.' || trimmed === './') {
    return getSpecWorkspacePath(document);
  }

  if (isRemoteContextReference(trimmed)) {
    return trimmed;
  }

  const expanded = expandUserPath(trimmed);
  if (path.isAbsolute(expanded)) {
    return path.normalize(expanded);
  }

  const base = getSpecWorkspacePath(document);
  return path.resolve(base, expanded);
}

function expandUserPath(input: string): string {
  if (!input) {
    return input;
  }
  if (input === '~') {
    return os.homedir();
  }
  if (input.startsWith('~/')) {
    return path.join(os.homedir(), input.slice(2));
  }
  return input;
}

async function getEmptyContextDir(): Promise<string> {
  if (!emptyContextDirReady) {
    emptyContextDirReady = fs
      .mkdir(emptyContextDirPath, { recursive: true })
      .then(() => emptyContextDirPath)
      .catch((error) => {
        void vscode.window.showWarningMessage(`Unable to prepare empty context directory: ${error}`);
        return emptyContextDirPath;
      });
  }
  return emptyContextDirReady;
}

function recordFromMap(map: Map<string, string>): Record<string, string> {
  const record: Record<string, string> = {};
  for (const [key, value] of map.entries()) {
    record[key] = value;
  }
  return record;
}

function getTerminalCommentPrefix(): string {
  const shell = vscode.env.shell?.toLowerCase() ?? '';
  if (shell.includes('cmd.exe')) {
    return 'REM';
  }
  return '#';
}

function clearCachedContextSelection(uri: vscode.Uri) {
  contextSelectionCache.delete(uri.toString());
}

function clearCachedArgsSelection(uri: vscode.Uri) {
  argsSelectionCache.delete(uri.toString());
}

function toInputValue(value: string | undefined): string {
  return value ?? '.';
}

class LastDalecActionState {
  private entry: LastDalecAction | undefined;

  record(entry: LastDalecAction) {
    this.entry = entry;
  }

  get(): LastDalecAction | undefined {
    return this.entry;
  }
}

interface LastDalecAction {
  type: 'build' | 'debug';
  target: string;
  specUri: vscode.Uri;
  contexts: ContextSelection;
  args: ArgsSelection;
}

function isRemoteContextReference(value: string): boolean {
  const lowered = value.toLowerCase();
  if (lowered.startsWith('type=')) {
    return true;
  }
  if (/^[a-z0-9+.-]+:\/\//i.test(value)) {
    return true;
  }
  if (value.startsWith('${')) {
    return true;
  }
  if (/[,:]/.test(value) && value.includes('=') && !value.includes(path.sep)) {
    return true;
  }
  return false;
}

async function rerunLastAction(
  tracker: DalecDocumentTracker,
  lastAction: LastDalecActionState,
  overrideType?: 'build' | 'debug',
) {
  const entry = lastAction.get();
  if (!entry) {
    void vscode.window.showInformationMessage('Dalec: no previous action to rerun.');
    return;
  }

  const document = await resolveDalecDocument(entry.specUri, tracker);
  if (!document) {
    return;
  }

  const metadata = tracker.getMetadata(document);
  const entryContexts = entry.contexts;
  const specContextNames = metadata?.contexts ?? [];
  const contextSelection =
    contextsSatisfied(entryContexts, specContextNames)
      ? entryContexts
      : await collectContextSelection(document, tracker, entryContexts);
  if (!contextSelection) {
    return;
  }

  const entryArgs = entry.args;
  const argsSelection = argsSatisfied(entryArgs, metadata?.args)
    ? entryArgs
    : await collectArgsSelection(document, tracker, entryArgs);
  if (!argsSelection) {
    return;
  }

  const folder = vscode.workspace.getWorkspaceFolder(entry.specUri);

  const actionType = overrideType ?? entry.type;

  if (actionType === 'debug') {
    const workspacePath = getWorkspaceRootForUri(entry.specUri);
    const configuration: DalecDebugConfiguration = {
      type: DEBUG_TYPE,
      name: `Dalec: Debug (${entry.target})`,
      request: 'launch',
      target: entry.target,
      specFile: getWorkspaceRelativeSpecPath(entry.specUri),
      context: contextSelection.defaultContextPath,
      buildContexts: recordFromMap(contextSelection.additionalContexts),
      buildArgs: recordFromMap(argsSelection.values),
      dalecContextResolved: true,
      workspaceFolder: workspacePath,
      cwd: workspacePath,
    };
    await vscode.debug.startDebugging(folder, configuration);
  } else {
    const dockerCommand = createDockerBuildxCommand({
      mode: 'build',
      target: entry.target,
      specFile: entry.specUri.fsPath,
      context: contextSelection.defaultContextPath,
      buildArgs: argsSelection.values,
      buildContexts: contextSelection.additionalContexts,
    });
    const formattedCommand = logDockerCommand('Build command', dockerCommand);
    const terminal = vscode.window.createTerminal({
      name: `Dalec Build (${entry.target})`,
      cwd: getWorkspaceRootForUri(entry.specUri),
      env: {
        ...process.env,
        BUILDX_EXPERIMENTAL: '1',
      },
    });
    terminal.show();
    terminal.sendText(`${getTerminalCommentPrefix()} Dalec command: ${formattedCommand}`);
    terminal.sendText(formattedCommand);
  }
}

function contextsSatisfied(selection: ContextSelection, requiredNames: string[]): boolean {
  const available = new Set(selection.additionalContexts.keys());
  available.add('context');
  for (const name of requiredNames) {
    if (!available.has(name)) {
      return false;
    }
  }
  return true;
}

function argsSatisfied(selection: ArgsSelection, definedArgs?: Map<string, string | undefined>): boolean {
  if (!definedArgs || definedArgs.size === 0) {
    return selection.values.size === 0;
  }
  if (selection.values.size === 0) {
    return false;
  }
  for (const key of definedArgs.keys()) {
    if (!selection.values.has(key)) {
      return false;
    }
  }
  return true;
}

function groupTargets(targets: BuildTargetInfo[]): Map<string, BuildTargetInfo[]> {
  const grouped = new Map<string, BuildTargetInfo[]>();
  for (const info of targets) {
    const scope = info.name.split('/')[0] || info.name;
    if (!grouped.has(scope)) {
      grouped.set(scope, []);
    }
    grouped.get(scope)!.push(info);
  }
  return grouped;
}

function isDebugScope(value: string): boolean {
  return value.toLowerCase() === 'debug';
}
