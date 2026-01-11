// XGrabba Background Service Worker

const DEFAULT_BACKEND_URL = 'http://localhost:9847';
const STORAGE_KEYS = {
  BACKEND_URL: 'backendUrl',
  API_KEY: 'apiKey',
  HISTORY: 'archiveHistory',
  SETTINGS: 'settings'
};

// Normalize URL by removing trailing slashes to prevent double-slash issues
function normalizeUrl(url) {
  if (!url) return url;
  return url.replace(/\/+$/, '');
}

// Initialize default settings on install
chrome.runtime.onInstalled.addListener(() => {
  chrome.storage.sync.get([STORAGE_KEYS.BACKEND_URL, STORAGE_KEYS.API_KEY], (result) => {
    if (!result[STORAGE_KEYS.BACKEND_URL]) {
      chrome.storage.sync.set({ [STORAGE_KEYS.BACKEND_URL]: DEFAULT_BACKEND_URL });
    }
  });

  chrome.storage.local.get([STORAGE_KEYS.HISTORY], (result) => {
    if (!result[STORAGE_KEYS.HISTORY]) {
      chrome.storage.local.set({ [STORAGE_KEYS.HISTORY]: [] });
    }
  });
});

// Message handler
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  handleMessage(message, sender).then(sendResponse);
  return true; // Keep channel open for async response
});

async function handleMessage(message, sender) {
  switch (message.type) {
    case 'ARCHIVE_VIDEO':
      return await archiveVideo(message.payload);

    case 'GET_HISTORY':
      return await getHistory();

    case 'CHECK_BACKEND':
      return await checkBackend();

    case 'GET_SETTINGS':
      return await getSettings();

    case 'SAVE_SETTINGS':
      return await saveSettings(message.payload);

    case 'RETRY_ARCHIVE':
      return await retryArchive(message.payload);

    case 'OPEN_UI':
      return await openUI(message.payload);

    default:
      return { error: 'Unknown message type' };
  }
}

async function openUI(payload = {}) {
  try {
    const { backendUrl, apiKey } = await getConfig();
    const path = payload.quick ? '/quick' : '/ui';
    let url = `${backendUrl}${path}`;

    // Add API key to URL for seamless auth
    if (apiKey) {
      url += `?key=${encodeURIComponent(apiKey)}`;
    }

    await chrome.tabs.create({ url });
    return { success: true };
  } catch (error) {
    console.error('Failed to open UI:', error);
    return { success: false, error: error.message };
  }
}

// Extract username from tweet URL
function extractUsername(tweetUrl) {
  if (!tweetUrl) return 'unknown';
  const match = tweetUrl.match(/(?:twitter\.com|x\.com)\/([^\/]+)\/status/i);
  return match ? match[1] : 'unknown';
}

async function archiveVideo(payload) {
  try {
    const { backendUrl, apiKey } = await getConfig();

    if (!apiKey) {
      return {
        success: false,
        error: 'API key not configured. Please set it in extension settings.'
      };
    }

    // New simplified API - just send the tweet URL, backend handles everything
    const response = await fetch(`${backendUrl}/api/v1/tweets`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': apiKey
      },
      body: JSON.stringify({
        tweet_url: payload.tweetUrl
      })
    });

    const data = await response.json();

    if (!response.ok) {
      throw new Error(data.error || 'Archive request failed');
    }

    const tweetId = data.tweet_id || payload.tweetId;
    const authorUsername = extractUsername(payload.tweetUrl);

    // Save to history with author info
    await addToHistory({
      tweetId: tweetId,
      tweetUrl: payload.tweetUrl,
      authorUsername: authorUsername,
      tweetTextPreview: 'Archiving...',
      status: 'pending',
      archivedAt: new Date().toISOString()
    });

    // Poll for status update in background
    pollForStatus(tweetId, backendUrl, apiKey);

    return {
      success: true,
      tweetId: tweetId,
      message: data.message
    };

  } catch (error) {
    console.error('Archive error:', error);

    // Save failed attempt to history
    await addToHistory({
      tweetId: payload.tweetId,
      tweetUrl: payload.tweetUrl,
      authorUsername: extractUsername(payload.tweetUrl),
      tweetTextPreview: error.message,
      status: 'failed',
      error: error.message,
      archivedAt: new Date().toISOString(),
      payload: payload // Save for retry
    });

    return {
      success: false,
      error: error.message
    };
  }
}

// Poll backend for status updates
async function pollForStatus(tweetId, backendUrl, apiKey) {
  const maxAttempts = 30; // 30 attempts * 2 seconds = 60 seconds max
  let attempts = 0;

  const poll = async () => {
    attempts++;
    try {
      const response = await fetch(`${backendUrl}/api/v1/tweets/${tweetId}`, {
        headers: { 'X-API-Key': apiKey }
      });

      if (response.ok) {
        const data = await response.json();

        // Update history entry
        await updateHistoryEntry(tweetId, {
          status: data.status === 'completed' ? 'success' : data.status,
          authorUsername: data.author || extractUsername(data.url),
          tweetTextPreview: data.text ? data.text.substring(0, 100) : 'Archived',
          aiTitle: data.ai_title
        });

        // Stop polling if completed or failed
        if (data.status === 'completed' || data.status === 'failed') {
          return;
        }
      }
    } catch (error) {
      console.error('Poll error:', error);
    }

    // Continue polling if not done and under max attempts
    if (attempts < maxAttempts) {
      setTimeout(poll, 2000);
    }
  };

  // Start polling after 1 second
  setTimeout(poll, 1000);
}

// Update a specific history entry
async function updateHistoryEntry(tweetId, updates) {
  const result = await chrome.storage.local.get([STORAGE_KEYS.HISTORY]);
  const history = result[STORAGE_KEYS.HISTORY] || [];

  const index = history.findIndex(h => h.tweetId === tweetId);
  if (index !== -1) {
    history[index] = { ...history[index], ...updates };
    await chrome.storage.local.set({ [STORAGE_KEYS.HISTORY]: history });
  }
}

async function getHistory() {
  // Fetch from API to get authoritative list with thumbnails
  try {
    const { backendUrl, apiKey } = await getConfig();
    if (!apiKey) {
      // Fall back to local storage if no API key
      const result = await chrome.storage.local.get([STORAGE_KEYS.HISTORY]);
      return { history: result[STORAGE_KEYS.HISTORY] || [] };
    }

    const response = await fetch(`${backendUrl}/api/v1/tweets?limit=20`, {
      headers: { 'X-API-Key': apiKey }
    });

    if (response.ok) {
      const data = await response.json();
      // Transform API response to history format
      const history = (data.tweets || []).map(tweet => ({
        tweetId: tweet.tweet_id,
        tweetUrl: tweet.url,
        authorUsername: tweet.author,
        authorAvatar: tweet.author_avatar,
        tweetTextPreview: tweet.text || tweet.ai_title || 'Archived',
        thumbnailUrl: tweet.media && tweet.media.length > 0 ? tweet.media[0].thumbnail_url : null,
        status: tweet.status === 'completed' ? 'success' : tweet.status,
        archivedAt: tweet.created_at
      }));
      return { history, fromApi: true };
    }
  } catch (error) {
    console.error('Failed to fetch history from API:', error);
  }

  // Fall back to local storage
  const result = await chrome.storage.local.get([STORAGE_KEYS.HISTORY]);
  return { history: result[STORAGE_KEYS.HISTORY] || [] };
}

async function addToHistory(entry) {
  const result = await chrome.storage.local.get([STORAGE_KEYS.HISTORY]);
  const history = result[STORAGE_KEYS.HISTORY] || [];

  // Add new entry at the beginning
  history.unshift(entry);

  // Keep only last 100 entries
  if (history.length > 100) {
    history.pop();
  }

  await chrome.storage.local.set({ [STORAGE_KEYS.HISTORY]: history });
}

async function checkBackend() {
  try {
    const { backendUrl } = await getConfig();
    const response = await fetch(`${backendUrl}/ready`, {
      method: 'GET',
      timeout: 5000
    });

    if (response.ok) {
      const data = await response.json();
      return {
        connected: true,
        queue: data.queue
      };
    }

    return { connected: false, error: 'Backend not ready' };
  } catch (error) {
    return { connected: false, error: error.message };
  }
}

async function getConfig() {
  const result = await chrome.storage.sync.get([
    STORAGE_KEYS.BACKEND_URL,
    STORAGE_KEYS.API_KEY
  ]);
  return {
    backendUrl: normalizeUrl(result[STORAGE_KEYS.BACKEND_URL] || DEFAULT_BACKEND_URL),
    apiKey: result[STORAGE_KEYS.API_KEY] || ''
  };
}

async function getSettings() {
  const result = await chrome.storage.sync.get([
    STORAGE_KEYS.BACKEND_URL,
    STORAGE_KEYS.API_KEY,
    STORAGE_KEYS.SETTINGS
  ]);
  return {
    backendUrl: normalizeUrl(result[STORAGE_KEYS.BACKEND_URL] || DEFAULT_BACKEND_URL),
    apiKey: result[STORAGE_KEYS.API_KEY] || '',
    settings: result[STORAGE_KEYS.SETTINGS] || {
      showToasts: true,
      markArchivedTweets: true
    }
  };
}

async function saveSettings(settings) {
  await chrome.storage.sync.set({
    [STORAGE_KEYS.BACKEND_URL]: normalizeUrl(settings.backendUrl),
    [STORAGE_KEYS.API_KEY]: settings.apiKey,
    [STORAGE_KEYS.SETTINGS]: settings.settings || {}
  });
  return { success: true };
}

async function retryArchive(payload) {
  // Remove from history first
  const result = await chrome.storage.local.get([STORAGE_KEYS.HISTORY]);
  const history = result[STORAGE_KEYS.HISTORY] || [];
  const index = history.findIndex(h => h.tweetId === payload.tweetId && h.status === 'failed');

  if (index !== -1) {
    const entry = history[index];
    history.splice(index, 1);
    await chrome.storage.local.set({ [STORAGE_KEYS.HISTORY]: history });

    // Retry with saved payload
    if (entry.payload) {
      return await archiveVideo(entry.payload);
    }
  }

  return { success: false, error: 'Could not retry - original request data not found' };
}

// Keyboard shortcut handler
chrome.commands.onCommand.addListener((command) => {
  if (command === 'archive-video') {
    // Send message to active tab's content script
    chrome.tabs.query({ active: true, currentWindow: true }, (tabs) => {
      if (tabs[0]) {
        chrome.tabs.sendMessage(tabs[0].id, { type: 'TRIGGER_ARCHIVE' });
      }
    });
  }
});
