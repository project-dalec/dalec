import { execFile, type ExecFileOptionsWithStringEncoding } from 'child_process';
import { promises as fs } from 'fs';
import * as os from 'os';
import * as path from 'path';
import { TextDecoder, promisify } from 'util';
import * as vscode from 'vscode';

const YAML_EXTENSION_ID = 'redhat.vscode-yaml';
const SCHEMA_SCHEME = 'dalecspec';
const DEBUG_TYPE = 'dalec-buildx';
const DEBUG_COMMAND = 'dalec.debugCurrentSpec';
const BUILD_COMMAND = 'dalec.buildCurrentSpec';
const DEBUG_ENABLED = process.env.DALEC_ENABLE_DEBUG === '1';
const DEBUG_DISABLED_MESSAGE =
  'Dalec BuildKit debugging is temporarily disabled. Set DALEC_ENABLE_DEBUG=1 before starting VS Code to re-enable it for development.';
const SYNTAX_REGEX = /^#\s*(?:syntax|sytnax)\s*=\s*(?<image>ghcr\.io\/(?:project-dalec|azure)\/dalec\/frontend:[^\s#]+|[^\s#]*dalec[^\s#]*)/i;
const FALLBACK_SCHEMA_RELATIVE_PATH = ['schemas', 'spec.schema.json'];
const FRONTEND_TARGET_CACHE_TTL_MS = 5 * 60 * 1000;
const execFileAsync = promisify(execFile);
const frontendTargetCache = new Map<string, FrontendTargetCacheEntry>();
const emptyContextDirPath = path.join(os.tmpdir(), 'dalec-empty-context');
let emptyContextDirReady: Promise<string> | undefined;
const contextSelectionCache = new Map<string, ContextSelection>();
const argsSelectionCache = new Map<string, ArgsSelection>();

export async function activate(context: vscode.ExtensionContext) {
  const tracker = new DalecDocumentTracker();
  context.subscriptions.push(tracker);
  const lastAction = new LastDalecActionState();

  const schemaProvider = new DalecSchemaProvider(context, tracker);
  await schemaProvider.initialize();
  context.subscriptions.push(schemaProvider);

  const codeLensProvider = new DalecCodeLensProvider(tracker, lastAction);
  context.subscriptions.push(
    vscode.languages.registerCodeLensProvider([{ language: 'yaml' }, { language: 'yml' }], codeLensProvider),
    codeLensProvider,
  );

  if (DEBUG_ENABLED) {
    const debugProvider = new DalecDebugConfigurationProvider(tracker);
    context.subscriptions.push(
      vscode.debug.registerDebugConfigurationProvider(DEBUG_TYPE, debugProvider),
      vscode.debug.registerDebugAdapterDescriptorFactory(
        DEBUG_TYPE,
        new DalecDebugAdapterDescriptorFactory(),
      ),
    );
  }

  context.subscriptions.push(
    vscode.commands.registerCommand(DEBUG_COMMAND, (uri?: vscode.Uri) => {
      if (!DEBUG_ENABLED) {
        void vscode.window.showInformationMessage(DEBUG_DISABLED_MESSAGE);
        return;
      }
      void runDebugCommand(uri, tracker, lastAction);
    }),
    vscode.commands.registerCommand(BUILD_COMMAND, (uri?: vscode.Uri) =>
      runBuildCommand(uri, tracker, lastAction),
    ),
    vscode.commands.registerCommand('dalec.rerunLastAction', () => rerunLastAction(tracker, lastAction)),
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

  private extractTargets(document: vscode.TextDocument): string[] {
    const targets = new Set<string>();
    const lines = document.getText().split(/\r?\n/);
    let inTargetsBlock = false;
    let baseIndent = 0;

    for (const rawLine of lines) {
      const line = rawLine.replace(/\t/g, '  ');

      if (!inTargetsBlock) {
        const match = line.match(/^(\s*)targets\s*:/);
        if (match) {
          inTargetsBlock = true;
          baseIndent = match[1].length;
        }
        continue;
      }

      if (!line.trim()) {
        continue;
      }

      if (line.trimStart().startsWith('#')) {
        continue;
      }

      const indent = line.match(/^(\s*)/)?.[1].length ?? 0;
      if (indent <= baseIndent) {
        break;
      }

      const keyMatch = line.match(/^\s*([^\s:#]+)\s*:/);
      if (keyMatch) {
        targets.add(keyMatch[1]);
      }
    }

    return [...targets];
  }

  private buildMetadata(document: vscode.TextDocument): DalecDocumentMetadata {
    return {
      targets: this.extractTargets(document),
      contexts: scanContextNames(document.getText()),
      args: extractArgs(document.getText()),
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

    return `${SCHEMA_SCHEME}://${authority}/spec`;
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

    if (DEBUG_ENABLED) {
      lenses.unshift(
        new vscode.CodeLens(range, {
          command: DEBUG_COMMAND,
          title: 'Dalec: Debug',
          arguments: [document.uri],
        }),
      );
    }

    const last = this.lastAction.get();
    if (last && last.specUri.toString() === document.uri.toString()) {
      if (last.type === 'debug' && !DEBUG_ENABLED) {
        // Skip showing rerun lens if debug runs are disabled.
        return lenses;
      }
      const label =
        last.type === 'build'
          ? `Prev Dalec: Build (${last.target})`
          : `Prev Dalec: Debug (${last.target})`;
      lenses.push(
        new vscode.CodeLens(range, {
          command: 'dalec.rerunLastAction',
          title: label,
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
    try {
      await vscode.workspace.fs.stat(vscode.Uri.file(resolvedSpec));
    } catch {
      void vscode.window.showErrorMessage(`Dalec spec file not found: ${resolvedSpec}`);
      return null;
    }

    config.specFile = resolvedSpec;

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
    const args = this.buildDockerArgs(config);

    const options: vscode.DebugAdapterExecutableOptions = {
      cwd: config.context,
      env: {
        ...process.env,
        BUILDX_EXPERIMENTAL: '1',
      },
    };

    return new vscode.DebugAdapterExecutable('docker', args, options);
  }

  private buildDockerArgs(config: DalecDebugConfiguration): string[] {
    const args = ['buildx', 'dap', 'build', '--target', config.target, '-f', config.specFile];

    if (config.buildArgs && typeof config.buildArgs === 'object') {
      for (const [key, value] of Object.entries(config.buildArgs)) {
        args.push('--build-arg', `${key}=${value}`);
      }
    }

    if (config.buildContexts && typeof config.buildContexts === 'object') {
      const contextsMap = new Map<string, string>(Object.entries(config.buildContexts));
      args.push(...buildContextArgs(contextsMap));
    }

    args.push(config.context);
    return args;
  }
}

interface DalecDebugConfiguration extends vscode.DebugConfiguration {
  target: string;
  specFile: string;
  context: string;
  buildArgs?: Record<string, string>;
  buildContexts?: Record<string, string>;
  dalecContextResolved?: boolean;
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

async function runDebugCommand(
  uri: vscode.Uri | undefined,
  tracker: DalecDocumentTracker,
  lastAction: LastDalecActionState,
) {
  if (!DEBUG_ENABLED) {
    void vscode.window.showInformationMessage(DEBUG_DISABLED_MESSAGE);
    return;
  }

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
  const configuration: DalecDebugConfiguration = {
    type: DEBUG_TYPE,
    name: `Dalec: Debug (${target})`,
    request: 'launch',
    target,
    specFile: document.uri.fsPath,
    context: contextSelection.defaultContextPath,
    buildContexts: recordFromMap(contextSelection.additionalContexts),
    dalecContextResolved: true,
    buildArgs: recordFromMap(argsSelection.values),
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

  const additionalContexts = formatBuildContextFlags(contextSelection.additionalContexts);
  const buildArgFlags = formatBuildArgs(argsSelection.values);
  const terminal = vscode.window.createTerminal({
    name: `Dalec Build (${target})`,
    env: {
      ...process.env,
      BUILDX_EXPERIMENTAL: '1',
    },
  });
  terminal.show();
  const parts = [
    'docker buildx build',
    ...additionalContexts,
    ...buildArgFlags,
    '--target',
    quote(target),
    '-f',
    quote(document.uri.fsPath),
    quote(contextSelection.defaultContextPath),
  ];
  terminal.sendText(parts.join(' '));

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

  if (targets.length === 1) {
    return targets[0].name;
  }

  if (targets.length > 1) {
    const items = buildTargetQuickPickItems(targets);
    const selection = await vscode.window.showQuickPick(items, {
      placeHolder: placeholder,
      matchOnDescription: true,
      matchOnDetail: true,
    });
    return selection?.target ?? undefined;
  }

  const manual = await vscode.window.showInputBox({
    prompt: 'No targets detected in this spec. Enter a target name to use.',
    placeHolder: 'target-name',
  });

  return manual?.trim() || undefined;
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

function formatBuildContextFlags(contexts: Map<string, string>): string[] {
  const entries = [...contexts.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  return entries.map(([name, ctxPath]) => `--build-context ${name}=${quote(ctxPath)}`);
}

function buildContextArgs(contexts: Map<string, string>): string[] {
  const entries = [...contexts.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  const args: string[] = [];
  for (const [name, ctxPath] of entries) {
    args.push('--build-context', `${name}=${ctxPath}`);
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
        const args = ['buildx', 'build', '--call', 'targets', '-f', document.uri.fsPath, contextPath];
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
  const contextNames = new Set(scanContextNames(document.getText()));

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

function scanContextNames(text: string): string[] {
  const contexts = new Set<string>();
  const lines = text.split(/\r?\n/);

  for (let i = 0; i < lines.length; i++) {
    const rawLine = lines[i];
    const line = rawLine.replace(/\t/g, '  ');
    if (line.trimStart().startsWith('#')) {
      continue;
    }

    const match = line.match(/^(\s*)(?:-\s*)?context\s*:\s*(.*)$/i);
    if (!match) {
      continue;
    }

    const indent = match[1].length;
    const remainder = match[2]?.trim() ?? '';
    const inlineName = extractInlineContextName(remainder);
    if (inlineName) {
      contexts.add(inlineName);
      continue;
    }

    const blockName = extractBlockContextName(lines, i + 1, indent);
    contexts.add(blockName ?? 'context');
  }

  return [...contexts];
}

function extractInlineContextName(remainder: string): string | undefined {
  if (!remainder) {
    return undefined;
  }
  if (remainder === '{}' || remainder === 'null' || remainder === '~') {
    return 'context';
  }

  if (!remainder.includes('{')) {
    return undefined;
  }

  const withoutComment = remainder.split('#')[0];
  const nameMatch = withoutComment.match(/name\s*:\s*([^,}]+)/i);
  if (!nameMatch) {
    return undefined;
  }
  return sanitizeContextName(nameMatch[1]);
}

async function rerunLastAction(tracker: DalecDocumentTracker, lastAction: LastDalecActionState) {
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
  const specContextNames = scanContextNames(document.getText());
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

  if (entry.type === 'debug') {
    if (!DEBUG_ENABLED) {
      void vscode.window.showInformationMessage(DEBUG_DISABLED_MESSAGE);
      return;
    }
    const configuration: DalecDebugConfiguration = {
      type: DEBUG_TYPE,
      name: `Dalec: Debug (${entry.target})`,
      request: 'launch',
      target: entry.target,
      specFile: entry.specUri.fsPath,
      context: contextSelection.defaultContextPath,
      buildContexts: recordFromMap(contextSelection.additionalContexts),
      buildArgs: recordFromMap(argsSelection.values),
      dalecContextResolved: true,
    };
    await vscode.debug.startDebugging(folder, configuration);
  } else {
    const additionalContexts = formatBuildContextFlags(contextSelection.additionalContexts);
    const buildArgFlags = formatBuildArgs(argsSelection.values);
    const terminal = vscode.window.createTerminal({
      name: `Dalec Build (${entry.target})`,
      env: {
        ...process.env,
        BUILDX_EXPERIMENTAL: '1',
      },
    });
    terminal.show();
    const parts = [
      'docker buildx build',
      ...additionalContexts,
      ...buildArgFlags,
      '--target',
      quote(entry.target),
      '-f',
      quote(entry.specUri.fsPath),
      quote(contextSelection.defaultContextPath),
    ];
    terminal.sendText(parts.join(' '));
  }
}

function extractBlockContextName(
  lines: string[],
  startIndex: number,
  baseIndent: number,
): string | undefined {
  for (let i = startIndex; i < lines.length; i++) {
    const candidate = lines[i].replace(/\t/g, '  ');
    const trimmed = candidate.trim();
    if (!trimmed || trimmed.startsWith('#')) {
      continue;
    }

    const indent = candidate.match(/^\s*/)?.[0].length ?? 0;
    if (indent <= baseIndent) {
      break;
    }

    const nameMatch = candidate.match(/^\s*name\s*:\s*(.+)$/i);
    if (nameMatch) {
      return sanitizeContextName(nameMatch[1]);
    }
  }
  return undefined;
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

function extractArgs(text: string): Map<string, string | undefined> {
  const args = new Map<string, string | undefined>();
  const lines = text.split(/\r?\n/);

  for (let i = 0; i < lines.length; i++) {
    const rawLine = lines[i];
    const line = rawLine.replace(/\t/g, '  ');
    if (line.trimStart().startsWith('#')) {
      continue;
    }
    const match = line.match(/^(\s*)args\s*:/);
    if (!match) {
      continue;
    }
    const baseIndent = match[1].length;
    for (let j = i + 1; j < lines.length; j++) {
      const candidateRaw = lines[j];
      const candidate = candidateRaw.replace(/\t/g, '  ');
      const trimmed = candidate.trim();
      if (!trimmed) {
        continue;
      }
      if (trimmed.startsWith('#')) {
        continue;
      }
      const indent = candidate.match(/^\s*/)?.[0].length ?? 0;
      if (indent <= baseIndent) {
        break;
      }

      const keyMatch = candidate.match(/^\s*([^\s:#]+)\s*:\s*(.*)$/);
      if (!keyMatch) {
        continue;
      }
      const key = keyMatch[1];
      let value = keyMatch[2]?.trim() ?? '';
      if (value.includes('#')) {
        value = value.split('#')[0]?.trim() ?? '';
      }
      if (
        (value.startsWith('"') && value.endsWith('"')) ||
        (value.startsWith("'") && value.endsWith("'"))
      ) {
        value = value.slice(1, -1);
      }
      args.set(key, value === '' ? undefined : value);
    }
    break;
  }

  return args;
}

function buildTargetQuickPickItems(targets: BuildTargetInfo[]): TargetQuickPickItem[] {
  const grouped = new Map<string, TargetQuickPickItem[]>();
  for (const info of targets) {
    const segments = info.name.split('/');
    const scope = segments[0];
    const label = segments.slice(1).join('/') || scope;
    const groupName = scope || info.name;
    if (!grouped.has(groupName)) {
      grouped.set(groupName, []);
    }
    grouped.get(groupName)!.push({
      label,
      detail: info.description,
      target: info.name,
    });
  }

  const items: TargetQuickPickItem[] = [];
  const sortedScopes = [...grouped.keys()].sort((a, b) => {
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

  for (const scope of sortedScopes) {
    items.push(createGroupHeader(scope));
    const entries = grouped.get(scope)!;
    entries.sort((a, b) => {
      const aDebug = isDebugTarget(a.target);
      const bDebug = isDebugTarget(b.target);
      if (aDebug && !bDebug) {
        return 1;
      }
      if (!aDebug && bDebug) {
        return -1;
      }
      const aNested = isNestedTarget(a.target);
      const bNested = isNestedTarget(b.target);
      if (aNested && !bNested) {
        return 1;
      }
      if (!aNested && bNested) {
        return -1;
      }
      return a.label.localeCompare(b.label);
    });
    items.push(...entries);
  }
  return items;
}

type TargetQuickPickItem = vscode.QuickPickItem & { target?: string };

function createGroupHeader(scope: string): TargetQuickPickItem {
  return {
    label: ` ${scope}`,
    kind: vscode.QuickPickItemKind.Separator,
  };
}

function isDebugScope(value: string): boolean {
  return value.toLowerCase() === 'debug';
}

function isDebugTarget(value?: string): boolean {
  if (!value) {
    return false;
  }
  const lower = value.toLowerCase();
  return (
    lower.startsWith('debug/') ||
    lower === 'debug' ||
    lower.includes('/debug/') ||
    lower.endsWith('/debug')
  );
}

function isNestedTarget(value?: string): boolean {
  if (!value) {
    return false;
  }
  return value.split('/').length > 2;
}
