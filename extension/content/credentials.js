// XGrabba Content Script - Browser Credentials Capture
// Extracts X.com authentication credentials for server-side GraphQL access

class CredentialsCapture {
  constructor() {
    this.lastSync = 0;
    this.syncInterval = 60000; // Sync at most once per minute
  }

  // Extract ct0 (CSRF token) from document cookies
  getCT0() {
    const match = document.cookie.match(/ct0=([^;]+)/);
    return match ? match[1] : null;
  }

  // Check if we have valid credentials to extract
  hasCredentials() {
    return this.getCT0() !== null;
  }

  // Get all capturable credentials
  // Note: auth_token is HttpOnly and requires cookies API in background script
  async getCredentials() {
    const ct0 = this.getCT0();
    if (!ct0) {
      return null;
    }

    // Request auth_token from background script (needs cookies permission)
    let authToken = null;
    try {
      const response = await chrome.runtime.sendMessage({ type: 'GET_AUTH_TOKEN' });
      authToken = response?.authToken || null;
    } catch (e) {
      console.debug('[XGrabba] Could not get auth_token from background:', e.message);
    }

    return {
      ct0,
      authToken,
      capturedAt: new Date().toISOString()
    };
  }

  // Sync credentials to the backend server
  async syncToServer() {
    // Rate limit syncing
    const now = Date.now();
    if (now - this.lastSync < this.syncInterval) {
      return;
    }

    // Check if credential forwarding is enabled
    try {
      const settings = await chrome.runtime.sendMessage({ type: 'GET_SETTINGS' });
      if (!settings?.settings?.forwardCredentials) {
        return;
      }
    } catch (e) {
      return;
    }

    const credentials = await this.getCredentials();
    if (!credentials || !credentials.ct0 || !credentials.authToken) {
      return;
    }

    try {
      const response = await chrome.runtime.sendMessage({
        type: 'SYNC_CREDENTIALS',
        payload: credentials
      });

      if (response?.success) {
        this.lastSync = now;
        console.debug('[XGrabba] Credentials synced successfully');
      }
    } catch (e) {
      console.debug('[XGrabba] Failed to sync credentials:', e.message);
    }
  }

  // Initialize credential capture
  // Called when content script loads on X.com
  init() {
    // Only run on X.com/Twitter pages when logged in
    if (!this.hasCredentials()) {
      return;
    }

    // Sync credentials on page load (with slight delay to ensure page is settled)
    setTimeout(() => this.syncToServer(), 2000);

    // Also sync when page becomes visible (user returns to tab)
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') {
        this.syncToServer();
      }
    });
  }
}

// Export for use by other content scripts
window.XGrabbaCredentials = new CredentialsCapture();

// Initialize when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => window.XGrabbaCredentials.init());
} else {
  window.XGrabbaCredentials.init();
}
