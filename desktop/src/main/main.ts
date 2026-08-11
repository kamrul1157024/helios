import { app, BrowserWindow, Menu, session, shell } from 'electron'
import path from 'node:path'

import { HostRegistry } from './hosts.ts'
import { Notifier, type NotifyTarget } from './notify.ts'
import { TerminalManager } from './terminals.ts'
import { registerIpc } from './ipc.ts'

/** dist/main → dist. The main bundle is CommonJS, so __dirname is the real thing. */
const distDir = path.join(__dirname, '..')
const rendererDir = path.join(distDir, 'renderer')

let window: BrowserWindow | null = null
let hosts: HostRegistry | null = null
let terminals: TerminalManager | null = null
let notifier: Notifier | null = null
let quitting = false

/**
 * One window at a time. A second launch — or a `helios://` link handed to the
 * OS — focuses the existing one rather than starting a second app with its own
 * set of terminal connections.
 */
if (!app.requestSingleInstanceLock()) {
  app.quit()
} else {
  app.on('second-instance', (_event, argv) => {
    focusWindow()
    const link = argv.find((arg) => arg.startsWith('helios://'))
    if (link) window?.webContents.send('app:open-pairing', link)
  })
  void start()
}

async function start(): Promise<void> {
  app.setAppUserModelId('dev.helios.desktop')
  if (process.defaultApp) {
    // Development: the executable is Electron itself, so the scheme has to be
    // registered against the script path to round-trip.
    app.setAsDefaultProtocolClient('helios', process.execPath, [path.resolve(process.argv[1] ?? '')])
  } else {
    app.setAsDefaultProtocolClient('helios')
  }

  await app.whenReady()
  hardenSession()

  hosts = new HostRegistry()
  terminals = new TerminalManager(hosts)
  notifier = new Notifier(hosts, activateNotification, () => {
    quitting = true
    app.quit()
  })

  registerIpc({ hosts, terminals, notifier, window: () => window })
  hosts.load()
  notifier.installTray(path.join(distDir, 'assets'))
  Menu.setApplicationMenu(buildMenu())

  createWindow()

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
    else focusWindow()
  })
  app.on('open-url', (event, url) => {
    event.preventDefault()
    focusWindow()
    window?.webContents.send('app:open-pairing', url)
  })
  app.on('before-quit', () => {
    quitting = true
    terminals?.closeAll()
    notifier?.destroy()
  })
  // The tray is the point of staying resident: closing the window should not
  // stop approvals from arriving.
  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin' && quitting) app.quit()
  })
}

function createWindow(): void {
  window = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 900,
    minHeight: 560,
    show: false,
    backgroundColor: '#101014',
    titleBarStyle: process.platform === 'darwin' ? 'hiddenInset' : 'default',
    webPreferences: {
      preload: path.join(distDir, 'preload', 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webviewTag: false,
      spellcheck: false,
    },
  })

  window.once('ready-to-show', () => window?.show())
  window.on('closed', () => {
    window = null
    terminals?.closeAll()
  })

  // Nothing in this app should navigate. Links open in the user's browser, and
  // an attempt to navigate the window itself is a bug or worse.
  window.webContents.setWindowOpenHandler(({ url }) => {
    if (url.startsWith('https://')) void shell.openExternal(url)
    return { action: 'deny' }
  })
  window.webContents.on('will-navigate', (event, url) => {
    if (url !== window?.webContents.getURL()) event.preventDefault()
  })

  void window.loadFile(path.join(rendererDir, 'index.html'))
}

function hardenSession(): void {
  // The renderer loads from file:// and talks to the daemon only through IPC,
  // so it needs no network of its own — and saying so means an injected script
  // has nowhere to send anything.
  session.defaultSession.webRequest.onHeadersReceived((details, callback) => {
    callback({
      responseHeaders: {
        ...details.responseHeaders,
        'Content-Security-Policy': [
          "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
            "img-src 'self' data:; font-src 'self' data:; connect-src 'none'; " +
            "object-src 'none'; base-uri 'none'; form-action 'none'",
        ],
      },
    })
  })

  session.defaultSession.setPermissionRequestHandler((_wc, _permission, callback) => callback(false))
}

function focusWindow(): void {
  if (!window) {
    createWindow()
    return
  }
  if (window.isMinimized()) window.restore()
  window.show()
  window.focus()
}

function activateNotification(target: NotifyTarget): void {
  focusWindow()
  if (target.sessionId) window?.webContents.send('app:activate-notification', target)
}

function buildMenu(): Menu {
  const isMac = process.platform === 'darwin'
  return Menu.buildFromTemplate([
    ...(isMac ? [{ role: 'appMenu' as const }] : []),
    {
      label: 'File',
      submenu: [
        {
          label: 'New Session…',
          accelerator: 'CmdOrCtrl+N',
          click: () => window?.webContents.send('app:activate-notification', { command: 'new-session' }),
        },
        { type: 'separator' },
        isMac ? { role: 'close' as const } : { role: 'quit' as const },
      ],
    },
    { role: 'editMenu' },
    {
      label: 'View',
      submenu: [
        { role: 'reload' as const },
        { role: 'toggleDevTools' as const },
        { type: 'separator' as const },
        { role: 'resetZoom' as const },
        { role: 'zoomIn' as const },
        { role: 'zoomOut' as const },
        { type: 'separator' as const },
        { role: 'togglefullscreen' as const },
      ],
    },
    { role: 'windowMenu' },
  ])
}
