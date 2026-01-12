// XGrabba Content Script - Hardened UI Injector
// Robust tweet button injection with reliability guarantees

class TweetInjector {
  constructor() {
    // Core state
    this.processedTweets = new Set();
    this.archiveStates = new Map(); // tweetId -> state
    this.archivedTweets = new Set(); // Known archived tweet IDs from backend

    // Observers
    this.mutationObserver = null;
    this.intersectionObserver = null;
    this.resizeObserver = null;

    // Queues for batched processing
    this.pendingTweets = new Set();
    this.processingScheduled = false;

    // Retry mechanism
    this.retryAttempts = 0;
    this.maxRetries = 10;
    this.retryDelay = 1000;

    // Performance tracking
    this.lastScanTime = 0;
    this.scanThrottleMs = 50;

    // Session ID for persistence
    this.sessionId = Date.now().toString(36);

    this.init();
  }

  init() {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => this.start());
    } else {
      this.start();
    }
  }

  async start() {
    console.log('[XGrabba] Starting injector v2.0');

    // Load archived tweets from backend
    await this.loadArchivedTweets();

    // Load processed tweets from session storage
    this.loadSessionState();

    // Initial scan
    this.scanForTweets();

    // Setup all observers
    this.setupMutationObserver();
    this.setupIntersectionObserver();
    this.setupScrollListener();

    // Listen for messages
    chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
      if (message.type === 'TRIGGER_ARCHIVE') {
        this.archiveVisibleTweet();
      } else if (message.type === 'ARCHIVE_STATUS_UPDATE') {
        this.handleStatusUpdate(message.payload);
      }
    });

    // Periodic re-scan as safety net (every 2 seconds)
    setInterval(() => this.scanForTweets(), 2000);

    // Periodic archived tweets refresh (every 30 seconds)
    setInterval(() => this.loadArchivedTweets(), 30000);
  }

  // ============================================================
  // ARCHIVED STATE MANAGEMENT
  // ============================================================

  async loadArchivedTweets() {
    try {
      const response = await chrome.runtime.sendMessage({ type: 'GET_ARCHIVED_IDS' });
      if (response && response.tweetIds) {
        response.tweetIds.forEach(id => this.archivedTweets.add(id));
        // Update any already-rendered buttons
        this.updateArchivedButtons();
      }
    } catch (error) {
      // Silently fail - this is an enhancement, not critical
      console.debug('[XGrabba] Could not load archived tweets:', error.message);
    }
  }

  updateArchivedButtons() {
    this.archivedTweets.forEach(tweetId => {
      const container = document.querySelector(`.xgrabba-btn-container[data-tweet-id="${tweetId}"]`);
      if (container) {
        const button = container.querySelector('button');
        if (button && !button.classList.contains('xgrabba-state-archived')) {
          this.setState(tweetId, button, 'archived');
        }
      }
    });
  }

  handleStatusUpdate(payload) {
    if (payload.status === 'completed' || payload.status === 'success') {
      this.archivedTweets.add(payload.tweetId);
      const container = document.querySelector(`.xgrabba-btn-container[data-tweet-id="${payload.tweetId}"]`);
      if (container) {
        const button = container.querySelector('button');
        if (button) {
          this.setState(payload.tweetId, button, 'archived');
        }
      }
    }
  }

  // ============================================================
  // SESSION PERSISTENCE
  // ============================================================

  loadSessionState() {
    try {
      const stored = sessionStorage.getItem('xgrabba_processed');
      if (stored) {
        const data = JSON.parse(stored);
        // Only use if from current page session
        if (data.url === window.location.pathname) {
          data.ids.forEach(id => this.processedTweets.add(id));
        }
      }
    } catch (e) {
      // Ignore errors
    }
  }

  saveSessionState() {
    try {
      sessionStorage.setItem('xgrabba_processed', JSON.stringify({
        url: window.location.pathname,
        ids: Array.from(this.processedTweets).slice(-500) // Keep last 500
      }));
    } catch (e) {
      // Ignore errors (quota exceeded, etc.)
    }
  }

  // ============================================================
  // MUTATION OBSERVER - TARGETED & EFFICIENT
  // ============================================================

  setupMutationObserver() {
    // Find the most specific container to observe
    const observeTarget = this.findTimelineContainer() || document.body;

    this.mutationObserver = new MutationObserver((mutations) => {
      // Quick filter: only care about added nodes
      let hasRelevantChanges = false;

      for (const mutation of mutations) {
        if (mutation.addedNodes.length > 0) {
          for (const node of mutation.addedNodes) {
            if (node.nodeType === Node.ELEMENT_NODE) {
              // Check if it's a tweet or contains tweets
              if (this.mightContainTweet(node)) {
                hasRelevantChanges = true;
                break;
              }
            }
          }
        }
        if (hasRelevantChanges) break;
      }

      if (hasRelevantChanges) {
        this.scheduleScan();
      }
    });

    this.mutationObserver.observe(observeTarget, {
      childList: true,
      subtree: true
    });

    // If we're observing body, also watch for timeline container to appear
    if (observeTarget === document.body) {
      this.watchForTimeline();
    }
  }

  findTimelineContainer() {
    // Try multiple selectors for the timeline container
    const selectors = [
      '[data-testid="primaryColumn"]',
      'main[role="main"]',
      '[aria-label*="Timeline"]',
      'section[role="region"]'
    ];

    for (const selector of selectors) {
      const element = document.querySelector(selector);
      if (element) {
        console.log('[XGrabba] Found timeline container:', selector);
        return element;
      }
    }

    return null;
  }

  watchForTimeline() {
    // Re-check periodically for timeline container
    const checkInterval = setInterval(() => {
      const container = this.findTimelineContainer();
      if (container && container !== document.body) {
        console.log('[XGrabba] Timeline container found, switching observer');
        this.mutationObserver.disconnect();
        this.mutationObserver.observe(container, {
          childList: true,
          subtree: true
        });
        clearInterval(checkInterval);
      }
    }, 500);

    // Stop checking after 30 seconds
    setTimeout(() => clearInterval(checkInterval), 30000);
  }

  mightContainTweet(node) {
    if (!node || !node.tagName) return false;

    // Direct tweet check
    if (node.tagName === 'ARTICLE' && node.hasAttribute('data-testid')) {
      return true;
    }

    // Container that might hold tweets
    if (node.querySelector && node.querySelector('article[data-testid="tweet"]')) {
      return true;
    }

    // Div/section that could be a tweet wrapper
    if (['DIV', 'SECTION'].includes(node.tagName) && node.children && node.children.length > 0) {
      return true;
    }

    return false;
  }

  // ============================================================
  // INTERSECTION OBSERVER - VIEWPORT AWARENESS
  // ============================================================

  setupIntersectionObserver() {
    this.intersectionObserver = new IntersectionObserver(
      (entries) => {
        entries.forEach(entry => {
          if (entry.isIntersecting) {
            const tweet = entry.target;
            // Inject button when tweet becomes visible
            if (!this.hasButton(tweet)) {
              this.injectArchiveButton(tweet);
            }
          }
        });
      },
      {
        root: null,
        rootMargin: '100px', // Pre-load buttons slightly before visible
        threshold: 0.1
      }
    );
  }

  hasButton(tweetElement) {
    return tweetElement.querySelector('.xgrabba-btn-container') !== null;
  }

  // ============================================================
  // SCROLL LISTENER - ADDITIONAL SAFETY NET
  // ============================================================

  setupScrollListener() {
    let scrollTimeout;
    window.addEventListener('scroll', () => {
      clearTimeout(scrollTimeout);
      scrollTimeout = setTimeout(() => {
        this.scanForTweets();
      }, 150);
    }, { passive: true });
  }

  // ============================================================
  // TWEET SCANNING - THROTTLED & BATCHED
  // ============================================================

  scheduleScan() {
    if (this.processingScheduled) return;

    const now = Date.now();
    const timeSinceLastScan = now - this.lastScanTime;

    if (timeSinceLastScan < this.scanThrottleMs) {
      // Throttle: schedule for later
      this.processingScheduled = true;
      setTimeout(() => {
        this.processingScheduled = false;
        this.scanForTweets();
      }, this.scanThrottleMs - timeSinceLastScan);
    } else {
      // Execute immediately
      this.scanForTweets();
    }
  }

  scanForTweets() {
    this.lastScanTime = Date.now();

    // Use multiple selector strategies for resilience
    const tweets = this.findAllTweets();
    let injectedCount = 0;

    tweets.forEach(tweet => {
      const tweetId = this.getTweetId(tweet);
      if (!tweetId) return;

      // Skip if already has our button
      if (this.hasButton(tweet)) {
        // But verify button is still correctly attached
        this.verifyButton(tweet, tweetId);
        return;
      }

      // Skip if already processed in this session
      if (this.processedTweets.has(tweetId)) {
        // Button might have been removed by Twitter's virtual scrolling
        // Re-inject if needed
        this.injectArchiveButton(tweet);
        return;
      }

      // Inject button
      if (this.injectArchiveButton(tweet)) {
        injectedCount++;
      }
    });

    if (injectedCount > 0) {
      this.saveSessionState();
    }
  }

  findAllTweets() {
    // Primary selector
    let tweets = document.querySelectorAll('article[data-testid="tweet"]');

    if (tweets.length === 0) {
      // Fallback selectors
      tweets = document.querySelectorAll('article[role="article"]');
    }

    if (tweets.length === 0) {
      // Last resort: look for tweet-like structures
      tweets = document.querySelectorAll('[data-testid="cellInnerDiv"] article');
    }

    return tweets;
  }

  verifyButton(tweetElement, tweetId) {
    const container = tweetElement.querySelector('.xgrabba-btn-container');
    if (!container) return;

    // Verify it's still properly attached to the action bar
    const actionBar = this.findActionBar(tweetElement);
    if (actionBar && !actionBar.contains(container)) {
      // Button was displaced, re-attach it
      actionBar.appendChild(container);
    }

    // Verify archived state is correct
    if (this.archivedTweets.has(tweetId)) {
      const button = container.querySelector('button');
      if (button && !button.classList.contains('xgrabba-state-archived')) {
        this.setState(tweetId, button, 'archived');
      }
    }
  }

  // ============================================================
  // BUTTON INJECTION - ROBUST WITH FALLBACKS
  // ============================================================

  injectArchiveButton(tweetElement) {
    const tweetId = this.getTweetId(tweetElement);
    if (!tweetId) return false;

    // Find action bar with fallbacks
    const actionBar = this.findActionBar(tweetElement);
    if (!actionBar) {
      // Schedule retry - action bar might load later
      this.scheduleRetry(tweetElement);
      return false;
    }

    // Don't double-inject
    if (actionBar.querySelector('.xgrabba-btn-container')) {
      return false;
    }

    this.processedTweets.add(tweetId);

    // Create button using requestAnimationFrame for smooth rendering
    requestAnimationFrame(() => {
      const container = this.createButtonContainer(tweetId);

      // Use insertBefore to place before the last item (usually share button)
      const lastChild = actionBar.lastElementChild;
      if (lastChild) {
        actionBar.insertBefore(container, lastChild);
      } else {
        actionBar.appendChild(container);
      }

      // Set initial state
      if (this.archivedTweets.has(tweetId)) {
        const button = container.querySelector('button');
        this.setState(tweetId, button, 'archived');
      }
    });

    return true;
  }

  findActionBar(tweetElement) {
    // Primary: role="group" is the action bar
    let actionBar = tweetElement.querySelector('[role="group"]:last-of-type');

    if (!actionBar) {
      // Fallback: look for the container with reply/retweet/like buttons
      actionBar = tweetElement.querySelector('[data-testid="reply"]')?.closest('[role="group"]');
    }

    if (!actionBar) {
      // Another fallback: find by aria-label patterns
      const groups = tweetElement.querySelectorAll('[role="group"]');
      for (const group of groups) {
        // The action bar typically has 4-5 buttons
        if (group.children.length >= 3) {
          actionBar = group;
          break;
        }
      }
    }

    return actionBar;
  }

  scheduleRetry(tweetElement) {
    // Use IntersectionObserver to retry when visible
    if (this.intersectionObserver) {
      this.intersectionObserver.observe(tweetElement);
    }
  }

  createButtonContainer(tweetId) {
    const container = document.createElement('div');
    container.className = 'xgrabba-btn-container';
    container.setAttribute('data-tweet-id', tweetId);

    const button = document.createElement('button');
    button.className = 'xgrabba-archive-btn';
    button.setAttribute('aria-label', 'Archive tweet with XGrabba');
    button.setAttribute('title', 'Archive tweet');
    button.setAttribute('type', 'button');
    button.innerHTML = this.getIdleIcon();

    // Prevent event bubbling to Twitter's handlers
    button.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      e.stopImmediatePropagation();
      this.handleArchiveClick(tweetId, button);
    }, true);

    // Prevent double-click issues
    button.addEventListener('dblclick', (e) => {
      e.preventDefault();
      e.stopPropagation();
    }, true);

    container.appendChild(button);
    return container;
  }

  // ============================================================
  // TWEET ID EXTRACTION - ROBUST
  // ============================================================

  getTweetId(tweetElement) {
    const extractor = window.XGrabbaExtractor;
    if (!extractor) {
      console.error('[XGrabba] Extractor not loaded');
      return null;
    }

    const url = extractor.getTweetUrl(tweetElement);
    return extractor.extractTweetId(url);
  }

  // ============================================================
  // ARCHIVE HANDLING
  // ============================================================

  async handleArchiveClick(tweetId, button) {
    const currentState = this.archiveStates.get(tweetId) || 'idle';

    if (currentState === 'saving') {
      return; // Already in progress
    }

    // Find tweet element
    const container = button.closest('.xgrabba-btn-container');
    const tweetElement = container?.closest('article[data-testid="tweet"]');

    if (!tweetElement) {
      this.showToast('Could not find tweet', 'error');
      return;
    }

    const extractor = window.XGrabbaExtractor;
    const tweetUrl = extractor.getTweetUrl(tweetElement);

    if (!tweetUrl) {
      this.showToast('Could not get tweet URL', 'error');
      return;
    }

    this.setState(tweetId, button, 'saving');

    try {
      const response = await chrome.runtime.sendMessage({
        type: 'ARCHIVE_VIDEO',
        payload: {
          tweetUrl: tweetUrl,
          tweetId: tweetId
        }
      });

      if (response.success) {
        this.setState(tweetId, button, 'success');
        this.showToast('Tweet archived successfully', 'success');

        // Add to archived set
        this.archivedTweets.add(tweetId);

        setTimeout(() => {
          this.setState(tweetId, button, 'archived');
        }, 2000);
      } else {
        this.setState(tweetId, button, 'failed');
        this.showToast(response.error || 'Archive failed', 'error');
      }
    } catch (error) {
      console.error('[XGrabba] Archive error:', error);
      this.setState(tweetId, button, 'failed');
      this.showToast('Archive failed: ' + error.message, 'error');
    }
  }

  archiveVisibleTweet() {
    const tweets = this.findAllTweets();

    for (const tweet of tweets) {
      const rect = tweet.getBoundingClientRect();
      const isVisible = rect.top >= 0 && rect.top <= window.innerHeight * 0.5;

      if (isVisible) {
        const tweetId = this.getTweetId(tweet);
        const button = tweet.querySelector('.xgrabba-archive-btn');
        if (button && tweetId) {
          this.handleArchiveClick(tweetId, button);
          break;
        }
      }
    }
  }

  // ============================================================
  // STATE MANAGEMENT
  // ============================================================

  setState(tweetId, button, state) {
    if (!button) return;

    this.archiveStates.set(tweetId, state);

    // Remove all state classes
    button.className = 'xgrabba-archive-btn xgrabba-state-' + state;
    button.disabled = state === 'saving';

    const icons = {
      idle: { icon: this.getIdleIcon(), title: 'Archive tweet' },
      saving: { icon: this.getSavingIcon(), title: 'Archiving tweet...' },
      success: { icon: this.getSuccessIcon(), title: 'Tweet archived successfully' },
      failed: { icon: this.getFailedIcon(), title: 'Archive failed - click to retry' },
      archived: { icon: this.getArchivedIcon(), title: 'Already archived - click to re-archive' }
    };

    const config = icons[state] || icons.idle;
    button.innerHTML = config.icon;
    button.setAttribute('title', config.title);
  }

  // ============================================================
  // TOAST NOTIFICATIONS
  // ============================================================

  showToast(message, type) {
    // Remove existing toast
    const existing = document.querySelector('.xgrabba-toast');
    if (existing) {
      existing.remove();
    }

    const toast = document.createElement('div');
    toast.className = `xgrabba-toast xgrabba-toast-${type}`;
    toast.textContent = message;
    toast.setAttribute('role', 'alert');

    document.body.appendChild(toast);

    // Force reflow for animation
    toast.offsetHeight;
    toast.classList.add('xgrabba-toast-visible');

    setTimeout(() => {
      toast.classList.remove('xgrabba-toast-visible');
      toast.classList.add('xgrabba-toast-fade');
      setTimeout(() => toast.remove(), 300);
    }, 3000);
  }

  // ============================================================
  // SVG ICONS
  // ============================================================

  getIdleIcon() {
    return `<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">
      <path d="M12 3v10.586l3.293-3.293 1.414 1.414L12 16.414l-4.707-4.707 1.414-1.414L12 13.586V3h2z"/>
      <path d="M5 17v4h14v-4h2v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4h2z"/>
    </svg>`;
  }

  getSavingIcon() {
    return `<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" class="xgrabba-spin" aria-hidden="true">
      <path d="M12 2a10 10 0 1 0 10 10A10 10 0 0 0 12 2zm0 18a8 8 0 1 1 8-8 8 8 0 0 1-8 8z" opacity="0.3"/>
      <path d="M12 4V2a10 10 0 0 1 10 10h-2a8 8 0 0 0-8-8z"/>
    </svg>`;
  }

  getSuccessIcon() {
    return `<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">
      <path d="M9 16.17L4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41L9 16.17z"/>
    </svg>`;
  }

  getFailedIcon() {
    return `<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">
      <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 15h-2v-2h2v2zm0-4h-2V7h2v6z"/>
    </svg>`;
  }

  getArchivedIcon() {
    return `<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">
      <path d="M12 3v10.586l3.293-3.293 1.414 1.414L12 16.414l-4.707-4.707 1.414-1.414L12 13.586V3h2z"/>
      <path d="M5 17v4h14v-4h2v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4h2z"/>
      <circle cx="18" cy="6" r="4" fill="#00BA7C"/>
      <path d="M16.5 6l1 1 2-2" stroke="white" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>`;
  }

  // ============================================================
  // CLEANUP
  // ============================================================

  destroy() {
    if (this.mutationObserver) {
      this.mutationObserver.disconnect();
    }
    if (this.intersectionObserver) {
      this.intersectionObserver.disconnect();
    }
  }
}

// Initialize injector
window.XGrabbaInjector = new TweetInjector();
