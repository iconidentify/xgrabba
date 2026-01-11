// XGrabba Popup Script

// Normalize URL by removing trailing slashes to prevent double-slash issues
function normalizeUrl(url) {
  if (!url) return url;
  return url.replace(/\/+$/, '');
}

document.addEventListener('DOMContentLoaded', () => {
  const app = new PopupApp();
  app.init();
});

class PopupApp {
  constructor() {
    this.elements = {
      settingsBtn: document.getElementById('settings-btn'),
      backBtn: document.getElementById('back-btn'),
      settingsPanel: document.getElementById('settings-panel'),
      mainContent: document.getElementById('main-content'),
      backendStatus: document.getElementById('backend-status'),
      archiveCount: document.getElementById('archive-count'),
      historyList: document.getElementById('history-list'),
      backendUrl: document.getElementById('backend-url'),
      apiKey: document.getElementById('api-key'),
      testConnection: document.getElementById('test-connection'),
      connectionStatus: document.getElementById('connection-status'),
      showToasts: document.getElementById('show-toasts'),
      markArchived: document.getElementById('mark-archived'),
      saveSettings: document.getElementById('save-settings'),
      clearHistory: document.getElementById('clear-history'),
      viewAllLink: document.getElementById('view-all-link'),
      quickArchiveLink: document.getElementById('quick-archive-link')
    };
  }

  async init() {
    this.bindEvents();
    await this.loadSettings();
    await this.checkBackend();
    await this.loadHistory();
  }

  bindEvents() {
    // Settings panel toggle
    this.elements.settingsBtn.addEventListener('click', () => {
      this.elements.settingsPanel.classList.remove('hidden');
    });

    this.elements.backBtn.addEventListener('click', () => {
      this.elements.settingsPanel.classList.add('hidden');
    });

    // Test connection
    this.elements.testConnection.addEventListener('click', () => {
      this.testConnection();
    });

    // Save settings
    this.elements.saveSettings.addEventListener('click', () => {
      this.saveSettings();
    });

    // Clear history
    this.elements.clearHistory.addEventListener('click', () => {
      this.clearHistory();
    });

    // View all link
    this.elements.viewAllLink.addEventListener('click', (e) => {
      e.preventDefault();
      this.openBackendUI(false);
    });

    // Quick archive link
    this.elements.quickArchiveLink.addEventListener('click', (e) => {
      e.preventDefault();
      this.openBackendUI(true);
    });
  }

  async loadSettings() {
    const response = await chrome.runtime.sendMessage({ type: 'GET_SETTINGS' });

    this.elements.backendUrl.value = response.backendUrl || 'http://localhost:9847';
    this.elements.apiKey.value = response.apiKey || '';
    this.elements.showToasts.checked = response.settings?.showToasts !== false;
    this.elements.markArchived.checked = response.settings?.markArchivedTweets !== false;
  }

  async saveSettings() {
    const settings = {
      backendUrl: normalizeUrl(this.elements.backendUrl.value.trim()),
      apiKey: this.elements.apiKey.value.trim(),
      settings: {
        showToasts: this.elements.showToasts.checked,
        markArchivedTweets: this.elements.markArchived.checked
      }
    };

    await chrome.runtime.sendMessage({
      type: 'SAVE_SETTINGS',
      payload: settings
    });

    // Show success feedback
    this.elements.saveSettings.textContent = 'Saved!';
    setTimeout(() => {
      this.elements.saveSettings.textContent = 'Save Settings';
    }, 2000);

    // Re-check backend with new settings
    await this.checkBackend();
  }

  async testConnection() {
    this.elements.connectionStatus.textContent = 'Testing...';
    this.elements.connectionStatus.className = 'connection-status';

    // Normalize URL to prevent double-slash issues
    const url = normalizeUrl(this.elements.backendUrl.value.trim());

    try {
      const response = await fetch(`${url}/ready`, {
        method: 'GET'
      });

      if (response.ok) {
        this.elements.connectionStatus.textContent = 'Connected successfully';
        this.elements.connectionStatus.className = 'connection-status success';
      } else {
        throw new Error(`Status ${response.status}`);
      }
    } catch (error) {
      this.elements.connectionStatus.textContent = `Failed: ${error.message}`;
      this.elements.connectionStatus.className = 'connection-status error';
    }
  }

  async checkBackend() {
    const statusEl = this.elements.backendStatus;
    statusEl.className = 'status-indicator status-checking';
    statusEl.querySelector('.status-text').textContent = 'Checking...';

    const response = await chrome.runtime.sendMessage({ type: 'CHECK_BACKEND' });

    if (response.connected) {
      statusEl.className = 'status-indicator status-connected';
      statusEl.querySelector('.status-text').textContent = 'Connected';
    } else {
      statusEl.className = 'status-indicator status-disconnected';
      statusEl.querySelector('.status-text').textContent = 'Disconnected';
    }
  }

  async loadHistory() {
    const response = await chrome.runtime.sendMessage({ type: 'GET_HISTORY' });
    const history = response.history || [];

    // Update count
    const successCount = history.filter(h => h.status === 'success' || h.status === 'pending').length;
    this.elements.archiveCount.textContent = `${successCount} archived`;

    // Render list
    if (history.length === 0) {
      this.elements.historyList.innerHTML = '<div class="empty-state">No archives yet</div>';
      return;
    }

    this.elements.historyList.innerHTML = history.slice(0, 10).map(item => this.renderHistoryItem(item)).join('');

    // Bind retry buttons
    this.elements.historyList.querySelectorAll('.retry-btn').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        this.retryArchive(btn.dataset.tweetId);
      });
    });

    // Bind item clicks to open tweet
    this.elements.historyList.querySelectorAll('.history-item').forEach(item => {
      item.addEventListener('click', () => {
        const url = item.dataset.tweetUrl;
        if (url) {
          chrome.tabs.create({ url });
        }
      });
    });
  }

  renderHistoryItem(item) {
    const timeAgo = this.formatTimeAgo(item.archivedAt);
    const statusIcon = this.getStatusIcon(item.status);
    const retryBtn = item.status === 'failed'
      ? `<button class="retry-btn" data-tweet-id="${item.tweetId}">Retry</button>`
      : '';

    return `
      <div class="history-item" data-tweet-url="${item.tweetUrl}">
        <div class="history-thumb">
          <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor">
            <path d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"/>
            <path d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z" fill="none" stroke="currentColor" stroke-width="2"/>
          </svg>
        </div>
        <div class="history-info">
          <div class="history-author">@${item.authorUsername}</div>
          <div class="history-text">${this.escapeHtml(item.tweetTextPreview || 'No text')}</div>
          <div class="history-time">${timeAgo}</div>
          ${retryBtn}
        </div>
        <div class="history-status ${item.status}">
          ${statusIcon}
        </div>
      </div>
    `;
  }

  getStatusIcon(status) {
    switch (status) {
      case 'success':
        return `<svg viewBox="0 0 24 24" fill="currentColor">
          <path d="M9 16.17L4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41L9 16.17z"/>
        </svg>`;
      case 'pending':
        return `<svg viewBox="0 0 24 24" fill="currentColor" class="spin">
          <path d="M12 2a10 10 0 1 0 10 10A10 10 0 0 0 12 2zm0 18a8 8 0 1 1 8-8 8 8 0 0 1-8 8z" opacity="0.3"/>
          <path d="M12 4V2a10 10 0 0 1 10 10h-2a8 8 0 0 0-8-8z"/>
        </svg>`;
      case 'failed':
        return `<svg viewBox="0 0 24 24" fill="currentColor">
          <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 15h-2v-2h2v2zm0-4h-2V7h2v6z"/>
        </svg>`;
      default:
        return '';
    }
  }

  formatTimeAgo(dateStr) {
    const date = new Date(dateStr);
    const now = new Date();
    const seconds = Math.floor((now - date) / 1000);

    if (seconds < 60) return 'Just now';
    if (seconds < 3600) return `${Math.floor(seconds / 60)} min ago`;
    if (seconds < 86400) return `${Math.floor(seconds / 3600)} hr ago`;
    return `${Math.floor(seconds / 86400)} days ago`;
  }

  escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  async retryArchive(tweetId) {
    const response = await chrome.runtime.sendMessage({
      type: 'RETRY_ARCHIVE',
      payload: { tweetId }
    });

    if (response.success) {
      await this.loadHistory();
    }
  }

  async clearHistory() {
    if (confirm('Clear all archive history?')) {
      await chrome.storage.local.set({ archiveHistory: [] });
      await this.loadHistory();
    }
  }

  async openBackendUI(quick = false) {
    try {
      const response = await chrome.runtime.sendMessage({
        type: 'OPEN_UI',
        payload: { quick }
      });
      // Close popup after opening UI
      if (response && response.success) {
        window.close();
      }
    } catch (error) {
      console.error('Failed to open UI:', error);
    }
  }
}
