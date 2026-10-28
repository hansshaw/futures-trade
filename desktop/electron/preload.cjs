const { contextBridge, ipcRenderer } = require('electron');
contextBridge.exposeInMainWorld('cornwatch', {
  request: (path, options) => ipcRenderer.invoke('cornwatch:request', path, options),
  captureChart: rect => ipcRenderer.invoke('cornwatch:capture-chart', rect),
  readJournalDrafts: () => ipcRenderer.sendSync('cornwatch:read-journal-drafts'),
  writeJournalDrafts: value => ipcRenderer.sendSync('cornwatch:write-journal-drafts', value),
  onUpdate: callback => {
    if (typeof callback !== 'function') throw new TypeError('callback 必须是函数');
    const listener = (_event, update) => callback(update);
    ipcRenderer.on('cornwatch:update', listener);
    return () => ipcRenderer.removeListener('cornwatch:update', listener);
  },
});
