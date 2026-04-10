import React from 'react';
import ReactDOM from 'react-dom/client';
import { App } from './App';
import { registerServiceWorker } from './lib/push';
import { initTheme } from './lib/theme';
import { deviceLog } from './lib/logger';
import './index.css';

// Intercept console.error/warn/log and forward to daemon
const origError = console.error;
const origWarn = console.warn;
const origLog = console.log;

console.error = (...args: unknown[]) => {
  origError.apply(console, args);
  deviceLog.error(args.map(String).join(' '), 'console');
};
console.warn = (...args: unknown[]) => {
  origWarn.apply(console, args);
  deviceLog.warn(args.map(String).join(' '), 'console');
};
console.log = (...args: unknown[]) => {
  origLog.apply(console, args);
  deviceLog.info(args.map(String).join(' '), 'console');
};

// Catch unhandled errors
window.addEventListener('error', (e) => {
  deviceLog.error(`${e.message} at ${e.filename}:${e.lineno}`, 'uncaught');
});
window.addEventListener('unhandledrejection', (e) => {
  deviceLog.error(`Unhandled rejection: ${e.reason}`, 'promise');
});

initTheme();

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);

registerServiceWorker();
