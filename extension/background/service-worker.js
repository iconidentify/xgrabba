// XGrabba Background Service Worker

const DEFAULT_BACKEND_URL = 'http://localhost:9847';
const STORAGE_KEYS = {
  BACKEND_URL: 'backendUrl',
  API_KEY: 'apiKey',
  HISTORY: 'archiveHistory',
  SETTINGS: 'settings',
  CREDENTIALS_STATUS: 'credentialsStatus'
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

// ============================================================================
// GraphQL interception (auto-sync query IDs / feature flags)
// ============================================================================
const GRAPHQL_CAPTURE = {
  queryIds: {},
  featureFlags: null,
  lastSync: 0,
  syncInterval: 60000, // at most once per minute
  lastMainJsFetch: 0,
  mainJsFetchInterval: 3600000 // fetch main.js every hour
};

// Proactively fetch query IDs from X's main.js bundle
// This ensures we have TweetResultByRestId even if user hasn't viewed a tweet
async function fetchQueryIdsFromMainJs() {
  const now = Date.now();
  if (now - GRAPHQL_CAPTURE.lastMainJsFetch < GRAPHQL_CAPTURE.mainJsFetchInterval) {
    return; // Rate limit
  }

  try {
    // Fetch X.com homepage to find main.js URL
    const homeResp = await fetch('https://x.com', {
      headers: { 'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36' }
    });
    const homeHtml = await homeResp.text();

    // Find main.js URL (pattern: https://abs.twimg.com/responsive-web/client-web/main.XXXXX.js)
    const mainJsMatch = homeHtml.match(/https:\/\/abs\.twimg\.com\/responsive-web\/client-web[^"]*?main\.[a-zA-Z0-9]+\.js/);
    if (!mainJsMatch) {
      console.debug('[XGrabba] Could not find main.js URL');
      return;
    }

    // Fetch main.js
    const jsResp = await fetch(mainJsMatch[0]);
    const jsContent = await jsResp.text();

    // Extract query IDs using regex patterns
    const patterns = [
      /queryId:"([a-zA-Z0-9_-]+)",operationName:"TweetResultByRestId"/,
      /queryId:"([a-zA-Z0-9_-]+)",operationName:"UserByRestId"/,
      /queryId:"([a-zA-Z0-9_-]+)",operationName:"UsersByRestIds"/,
      /queryId:"([a-zA-Z0-9_-]+)",operationName:"Bookmarks"/,
      /queryId:"([a-zA-Z0-9_-]+)",operationName:"TweetDetail"/,
      /operationName:"TweetResultByRestId"[^}]*queryId:"([a-zA-Z0-9_-]+)"/,
      /operationName:"UserByRestId"[^}]*queryId:"([a-zA-Z0-9_-]+)"/,
      /operationName:"UsersByRestIds"[^}]*queryId:"([a-zA-Z0-9_-]+)"/,
      /operationName:"Bookmarks"[^}]*queryId:"([a-zA-Z0-9_-]+)"/,
      /operationName:"TweetDetail"[^}]*queryId:"([a-zA-Z0-9_-]+)"/
    ];

    const operations = ['TweetResultByRestId', 'UserByRestId', 'UsersByRestIds', 'Bookmarks', 'TweetDetail'];
    let foundCount = 0;

    for (let i = 0; i < patterns.length; i++) {
      const match = jsContent.match(patterns[i]);
      if (match && match[1]) {
        const opName = operations[i % operations.length];
        const prev = GRAPHQL_CAPTURE.queryIds[opName];
        if (prev !== match[1]) {
          GRAPHQL_CAPTURE.queryIds[opName] = match[1];
          console.debug('[XGrabba] Fetched queryId from main.js', { operationName: opName, queryId: match[1] });
          foundCount++;
        }
      }
    }

    GRAPHQL_CAPTURE.lastMainJsFetch = now;
    console.debug('[XGrabba] main.js fetch complete', { foundQueryIds: foundCount, total: Object.keys(GRAPHQL_CAPTURE.queryIds).length });

    // Sync to backend if we found new IDs
    if (foundCount > 0) {
      void maybeSyncGraphQLCapture();
    }
  } catch (e) {
    console.debug('[XGrabba] Failed to fetch main.js:', e.message);
  }
}

// Fetch query IDs on startup and periodically
fetchQueryIdsFromMainJs();
setInterval(fetchQueryIdsFromMainJs, 3600000); // Every hour

// Observe X GraphQL requests and capture queryId/operationName/features.
// This keeps the backend resilient to X rotating query IDs and feature flags.
chrome.webRequest.onBeforeRequest.addListener(
  (details) => {
    try {
      recordGraphQLRequest(details.url);
    } catch (e) {
      // best-effort only
    }
  },
  {
    urls: [
      'https://x.com/i/api/graphql/*',
      'https://twitter.com/i/api/graphql/*'
    ]
  }
);

function recordGraphQLRequest(requestUrl) {
  const u = new URL(requestUrl);
  const parts = u.pathname.split('/').filter(Boolean);
  const gqlIdx = parts.indexOf('graphql');
  if (gqlIdx === -1 || parts.length < gqlIdx + 3) return;

  const queryId = parts[gqlIdx + 1];
  const operationName = parts[gqlIdx + 2];
  if (!queryId || !operationName) return;

  const prev = GRAPHQL_CAPTURE.queryIds[operationName];
  if (prev !== queryId) {
    GRAPHQL_CAPTURE.queryIds[operationName] = queryId;
    console.debug('[XGrabba] Captured GraphQL queryId', { operationName, queryId });
  }

  const featuresParam = u.searchParams.get('features');
  if (featuresParam) {
    // searchParams already decodes; parse to ensure it's valid JSON.
    try {
      GRAPHQL_CAPTURE.featureFlags = JSON.parse(featuresParam);
      console.debug('[XGrabba] Captured GraphQL feature flags', { bytes: featuresParam.length });
    } catch (e) {
      // ignore malformed features
      console.debug('[XGrabba] Failed to parse GraphQL feature flags', e?.message);
    }
  }

  // Best-effort sync (rate-limited) if forwarding enabled.
  void maybeSyncGraphQLCapture();
}

async function maybeSyncGraphQLCapture() {
  const now = Date.now();
  if (now - GRAPHQL_CAPTURE.lastSync < GRAPHQL_CAPTURE.syncInterval) return;

  const resp = await getSettings().catch(() => null);
  if (!resp?.settings?.forwardCredentials) return;

  const auth = await getAuthToken();
  const ct0 = await getCT0Token();
  if (!auth?.authToken || !ct0?.ct0) {
    console.debug('[XGrabba] Skipping GraphQL capture sync (missing cookies)', { hasAuth: !!auth?.authToken, hasCT0: !!ct0?.ct0 });
    return;
  }

  // Get ALL cookies for full session access (needed for age-restricted content)
  const allCookies = await getAllCookies();

  const result = await syncCredentials({
    authToken: auth.authToken,
    ct0: ct0.ct0,
    cookies: allCookies.cookies,  // Full cookie string for NSFW/age-restricted content
    queryIds: GRAPHQL_CAPTURE.queryIds,
    featureFlags: GRAPHQL_CAPTURE.featureFlags
  });

  if (result?.success) {
    GRAPHQL_CAPTURE.lastSync = now;
    console.debug('[XGrabba] Synced credentials+GraphQL capture', {
      queryIdCount: Object.keys(GRAPHQL_CAPTURE.queryIds || {}).length,
      hasFeatureFlags: !!GRAPHQL_CAPTURE.featureFlags,
      cookieCount: allCookies.count
    });
  }
}

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

    case 'GET_ARCHIVED_IDS':
      return await getArchivedTweetIds();

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

    case 'GET_AUTH_TOKEN':
      return await getAuthToken();

    case 'SYNC_CREDENTIALS':
      return await syncCredentials(message.payload);

    case 'GET_CREDENTIALS_STATUS':
      return await getCredentialsStatus();
    
    case 'GET_GRAPHQL_CAPTURE':
      return getGraphQLCapture();

    default:
      return { error: 'Unknown message type' };
  }
}

function getGraphQLCapture() {
  return {
    queryIds: GRAPHQL_CAPTURE.queryIds || {},
    queryIdCount: Object.keys(GRAPHQL_CAPTURE.queryIds || {}).length,
    hasFeatureFlags: !!GRAPHQL_CAPTURE.featureFlags,
    featureFlagsBytes: GRAPHQL_CAPTURE.featureFlags ? JSON.stringify(GRAPHQL_CAPTURE.featureFlags).length : 0,
    lastSync: GRAPHQL_CAPTURE.lastSync ? new Date(GRAPHQL_CAPTURE.lastSync).toISOString() : null
  };
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
          // Notify content scripts of the status change
          await notifyContentScripts(tweetId, data.status);
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

// Get list of archived tweet IDs for content script to mark buttons
async function getArchivedTweetIds() {
  try {
    const { backendUrl, apiKey } = await getConfig();
    if (!apiKey) {
      // Fall back to local history
      const result = await chrome.storage.local.get([STORAGE_KEYS.HISTORY]);
      const history = result[STORAGE_KEYS.HISTORY] || [];
      const tweetIds = history
        .filter(h => h.status === 'success' || h.status === 'completed')
        .map(h => h.tweetId);
      return { tweetIds };
    }

    // Fetch more tweets for better coverage
    const response = await fetch(`${backendUrl}/api/v1/tweets?limit=200`, {
      headers: { 'X-API-Key': apiKey }
    });

    if (response.ok) {
      const data = await response.json();
      const tweetIds = (data.tweets || [])
        .filter(t => t.status === 'completed')
        .map(t => t.tweet_id);
      return { tweetIds };
    }
  } catch (error) {
    console.debug('Could not fetch archived IDs:', error.message);
  }

  // Fallback to local storage
  const result = await chrome.storage.local.get([STORAGE_KEYS.HISTORY]);
  const history = result[STORAGE_KEYS.HISTORY] || [];
  const tweetIds = history
    .filter(h => h.status === 'success' || h.status === 'completed')
    .map(h => h.tweetId);
  return { tweetIds };
}

// Notify content scripts of status updates
async function notifyContentScripts(tweetId, status) {
  try {
    const tabs = await chrome.tabs.query({ url: ['https://x.com/*', 'https://twitter.com/*'] });
    for (const tab of tabs) {
      try {
        await chrome.tabs.sendMessage(tab.id, {
          type: 'ARCHIVE_STATUS_UPDATE',
          payload: { tweetId, status }
        });
      } catch (e) {
        // Tab might not have content script, ignore
      }
    }
  } catch (error) {
    console.debug('Could not notify content scripts:', error.message);
  }
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

// ============================================================================
// Browser Credentials Management
// ============================================================================

// Get auth_token cookie from X.com using cookies API
async function getAuthToken() {
  try {
    const cookie = await chrome.cookies.get({
      url: 'https://x.com',
      name: 'auth_token'
    });

    if (cookie && cookie.value) {
      return { authToken: cookie.value };
    }

    // Try twitter.com as fallback
    const twitterCookie = await chrome.cookies.get({
      url: 'https://twitter.com',
      name: 'auth_token'
    });

    if (twitterCookie && twitterCookie.value) {
      return { authToken: twitterCookie.value };
    }

    return { authToken: null };
  } catch (error) {
    console.error('Failed to get auth_token:', error);
    return { authToken: null, error: error.message };
  }
}

// Get ct0 cookie from X.com using cookies API
async function getCT0Token() {
  try {
    const cookie = await chrome.cookies.get({
      url: 'https://x.com',
      name: 'ct0'
    });

    if (cookie && cookie.value) {
      return { ct0: cookie.value };
    }

    // Try twitter.com as fallback
    const twitterCookie = await chrome.cookies.get({
      url: 'https://twitter.com',
      name: 'ct0'
    });

    if (twitterCookie && twitterCookie.value) {
      return { ct0: twitterCookie.value };
    }

    return { ct0: null };
  } catch (error) {
    console.error('Failed to get ct0:', error);
    return { ct0: null, error: error.message };
  }
}

// Get ALL cookies from X.com/Twitter.com for full session access
// This is needed for age-restricted/NSFW content which requires additional session cookies
async function getAllCookies() {
  try {
    // Get cookies from both domains.
    //
    // Important: use `url` filtering (not `domain`) so we capture cookies that are scoped
    // to `x.com` (no leading dot) as well as `.x.com`, including HttpOnly cookies that
    // can be required for NSFW / age-restricted GraphQL endpoints (Cloudflare, etc).
    const xCookies = await chrome.cookies.getAll({ url: 'https://x.com/' });
    const twitterCookies = await chrome.cookies.getAll({ url: 'https://twitter.com/' });

    // Merge cookies, preferring x.com cookies over twitter.com for duplicates
    const cookieMap = new Map();

    // Add twitter.com cookies first (will be overwritten by x.com if same name)
    for (const cookie of twitterCookies) {
      cookieMap.set(cookie.name, cookie.value);
    }

    // Add x.com cookies (these take precedence)
    for (const cookie of xCookies) {
      cookieMap.set(cookie.name, cookie.value);
    }

    // Build cookie string
    const cookieString = Array.from(cookieMap.entries())
      .map(([name, value]) => `${name}=${value}`)
      .join('; ');

    console.debug('[XGrabba] Captured cookies', {
      xCount: xCookies.length,
      twitterCount: twitterCookies.length,
      totalUnique: cookieMap.size
    });

    return { cookies: cookieString, count: cookieMap.size };
  } catch (error) {
    console.error('Failed to get all cookies:', error);
    return { cookies: null, count: 0, error: error.message };
  }
}

// Sync browser credentials to backend server
async function syncCredentials(credentials) {
  try {
    const { backendUrl, apiKey } = await getConfig();

    if (!apiKey) {
      return { success: false, error: 'API key not configured' };
    }

    if (!credentials || !credentials.ct0 || !credentials.authToken) {
      return { success: false, error: 'Incomplete credentials' };
    }

    // Best-effort enrichment:
    // - content scripts only have `ct0` and `authToken`
    // - background worker may have captured full cookies, queryIds, and featureFlags
    // Always try to send the richest payload to support NSFW/age-restricted content.
    let cookieString = credentials.cookies;
    let cookieCount = 0;
    if (!cookieString) {
      const allCookies = await getAllCookies();
      cookieString = allCookies.cookies;
      cookieCount = allCookies.count || 0;
    }

    const queryIds = (credentials.queryIds && Object.keys(credentials.queryIds).length > 0)
      ? credentials.queryIds
      : (GRAPHQL_CAPTURE.queryIds || {});

    const featureFlags = credentials.featureFlags || GRAPHQL_CAPTURE.featureFlags;

    const body = {
      auth_token: credentials.authToken,
      ct0: credentials.ct0
    };
    // Include full cookie string for age-restricted/NSFW content support
    if (cookieString) {
      body.cookies = cookieString;
    }
    if (queryIds && Object.keys(queryIds).length > 0) {
      body.query_ids = queryIds;
    }
    if (featureFlags) {
      body.feature_flags = featureFlags;
    }

    const response = await fetch(`${backendUrl}/api/v1/extension/credentials`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': apiKey
      },
      body: JSON.stringify(body)
    });

    if (!response.ok) {
      const data = await response.json().catch(() => ({}));
      throw new Error(data.error || `HTTP ${response.status}`);
    }

    const data = await response.json();

    // Update local status
    await chrome.storage.local.set({
      [STORAGE_KEYS.CREDENTIALS_STATUS]: {
        synced: true,
        lastSync: new Date().toISOString(),
        error: null
      }
    });

    if (cookieCount > 0) {
      console.debug('[XGrabba] Synced credentials (enriched)', {
        cookieCount,
        queryIdCount: Object.keys(queryIds || {}).length,
        hasFeatureFlags: !!featureFlags
      });
    }

    return { success: true, status: data.status };
  } catch (error) {
    console.error('Failed to sync credentials:', error);

    // Update local status with error
    await chrome.storage.local.set({
      [STORAGE_KEYS.CREDENTIALS_STATUS]: {
        synced: false,
        lastSync: null,
        error: error.message
      }
    });

    return { success: false, error: error.message };
  }
}

// Get current credentials sync status
async function getCredentialsStatus() {
  try {
    const result = await chrome.storage.local.get([STORAGE_KEYS.CREDENTIALS_STATUS]);
    const status = result[STORAGE_KEYS.CREDENTIALS_STATUS] || {
      synced: false,
      lastSync: null,
      error: null
    };

    // Also check if we have auth_token available
    const authResult = await getAuthToken();
    const hasAuthToken = !!authResult.authToken;

    return {
      ...status,
      hasAuthToken,
      canSync: hasAuthToken
    };
  } catch (error) {
    return {
      synced: false,
      lastSync: null,
      error: error.message,
      hasAuthToken: false,
      canSync: false
    };
  }
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
