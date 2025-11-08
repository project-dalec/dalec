import { execFile, type ExecFileOptionsWithStringEncoding } from 'child_process';
import * as path from 'path';
import { TextDecoder, promisify } from 'util';
import * as vscode from 'vscode';

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

export async function activate(context: vscode.ExtensionContext) {
  const tracker = new DalecDocumentTracker();
  context.subscriptions.push(tracker);

  const schemaProvider = new DalecSchemaProvider(context, tracker);
  await schemaProvider.initialize();
  context.subscriptions.push(schemaProvider);

  const codeLensProvider = new DalecCodeLensProvider(tracker);
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
  );

  context.subscriptions.push(
    vscode.commands.registerCommand(DEBUG_COMMAND, (uri?: vscode.Uri) =>
      runDebugCommand(uri, tracker),
    ),
    vscode.commands.registerCommand(BUILD_COMMAND, (uri?: vscode.Uri) =>
      runBuildCommand(uri, tracker),
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

    const metadata: DalecDocumentMetadata = {
      targets: this.extractTargets(document),
    };

    const key = document.uri.toString();
    this.tracked.set(key, metadata);
    this.changeEmitter.fire(document.uri);
  }

  private delete(uri: vscode.Uri) {
    const key = uri.toString();
    if (this.tracked.delete(key)) {
      this.changeEmitter.fire(uri);
    }
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

  constructor(private readonly tracker: DalecDocumentTracker) {
    this.trackerSubscription = this.tracker.onDidChange(() => this.emitter.fire());
  }

  provideCodeLenses(document: vscode.TextDocument): vscode.CodeLens[] | undefined {
    if (!this.tracker.isDalecDocument(document)) {
      return undefined;
    }

    const range = new vscode.Range(0, 0, 0, 0);
    return [
      new vscode.CodeLens(range, {
        command: DEBUG_COMMAND,
        title: 'Dalec: Debug',
        arguments: [document.uri],
      }),
      new vscode.CodeLens(range, {
        command: BUILD_COMMAND,
        title: 'Dalec: Build',
        arguments: [document.uri],
      }),
    ];
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

    if (typeof config.context !== 'string' || !config.context) {
      const fallback = folder?.uri.fsPath;
      if (fallback) {
        config.context = fallback;
      } else if (!this.containsVariableReference(specFile)) {
        config.context = path.dirname(specFile);
      }
    }

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

    let contextValue = typeof config.context === 'string' ? config.context.trim() : '';
    if (!contextValue) {
      contextValue = path.dirname(resolvedSpec);
    }
    config.context = this.resolvePath(contextValue, folder);

    if (config.buildArgs && typeof config.buildArgs !== 'object') {
      void vscode.window.showWarningMessage('Ignoring buildArgs – value must be an object map.');
      delete config.buildArgs;
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

    args.push(config.context);
    return args;
  }
}

interface DalecDebugConfiguration extends vscode.DebugConfiguration {
  target: string;
  specFile: string;
  context: string;
  buildArgs?: Record<string, string>;
}

interface DalecDocumentMetadata {
  targets: string[];
}

async function runDebugCommand(uri: vscode.Uri | undefined, tracker: DalecDocumentTracker) {
  const document = await resolveDalecDocument(uri, tracker);
  if (!document) {
    return;
  }

  const target = await pickTarget(document, tracker, 'Select a Dalec target to debug');
  if (!target) {
    return;
  }

  const folder = vscode.workspace.getWorkspaceFolder(document.uri);
  const configuration: DalecDebugConfiguration = {
    type: DEBUG_TYPE,
    name: `Dalec: Debug ${target}`,
    request: 'launch',
    target,
    specFile: document.uri.fsPath,
    context: folder?.uri.fsPath ?? path.dirname(document.uri.fsPath),
  };

  await vscode.debug.startDebugging(folder, configuration);
}

async function runBuildCommand(uri: vscode.Uri | undefined, tracker: DalecDocumentTracker) {
  const document = await resolveDalecDocument(uri, tracker);
  if (!document) {
    return;
  }

  const target = await pickTarget(document, tracker, 'Select a Dalec target to build');
  if (!target) {
    return;
  }

  const contextPath = getContextPath(document);
  const terminal = vscode.window.createTerminal({
    name: `Dalec Build (${target})`,
    env: {
      ...process.env,
      BUILDX_EXPERIMENTAL: '1',
    },
  });
  terminal.show();
  terminal.sendText(
    `docker buildx build --target ${quote(target)} -f ${quote(document.uri.fsPath)} ${quote(contextPath)}`,
  );
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
    return targets[0];
  }

  if (targets.length > 1) {
    const selection = await vscode.window.showQuickPick(targets, {
      placeHolder: placeholder,
    });
    return selection ?? undefined;
  }

  const manual = await vscode.window.showInputBox({
    prompt: 'No targets detected in this spec. Enter a target name to use.',
    placeHolder: 'target-name',
  });

  return manual?.trim() || undefined;
}

function getContextPath(document: vscode.TextDocument): string {
  const folder = vscode.workspace.getWorkspaceFolder(document.uri);
  return folder?.uri.fsPath ?? path.dirname(document.uri.fsPath);
}

function quote(value: string): string {
  if (value.includes(' ')) {
    return `"${value.replace(/(["\\$`])/g, '\\$1')}"`;
  }
  return value;
}

async function getTargetsForDocument(
  document: vscode.TextDocument,
  tracker: DalecDocumentTracker,
): Promise<string[]> {
  const targets = new Set(tracker.getMetadata(document)?.targets ?? []);
  const frontendTargets = await getFrontendTargets(document);
  frontendTargets?.forEach((target) => targets.add(target));
  return [...targets];
}

async function getFrontendTargets(document: vscode.TextDocument): Promise<string[] | undefined> {
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
        const contextPath = getContextPath(document);
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

function parseTargetsFromOutput(output: string): string[] {
  const targets = new Set<string>();
  for (const line of output.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || /^target/i.test(trimmed) || trimmed.startsWith('=') || trimmed.startsWith('-')) {
      continue;
    }

    const match = trimmed.match(/^([A-Za-z0-9._/-]+)/);
    if (match) {
      targets.add(match[1]);
    }
  }
  return [...targets];
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
  targets: string[];
  timestamp: number;
}
