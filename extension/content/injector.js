// XGrabba Content Script - UI Injector

class TweetInjector {
  constructor() {
    this.processedTweets = new Set();
    this.archiveStates = new Map(); // tweetId -> state
    this.observer = null;

    this.init();
  }

  init() {
    // Wait for page to be ready
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => this.start());
    } else {
      this.start();
    }
  }

  start() {
    // Initial scan
    this.scanForTweets();

    // Setup mutation observer for dynamically loaded tweets
    this.setupObserver();

    // Listen for messages from background/popup
    chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
      if (message.type === 'TRIGGER_ARCHIVE') {
        this.archiveVisibleVideo();
      }
    });
  }

  setupObserver() {
    this.observer = new MutationObserver((mutations) => {
      // Debounce to avoid excessive processing
      clearTimeout(this.scanTimeout);
      this.scanTimeout = setTimeout(() => this.scanForTweets(), 100);
    });

    this.observer.observe(document.body, {
      childList: true,
      subtree: true
    });
  }

  scanForTweets() {
    // Find all tweet articles
    const tweets = document.querySelectorAll('article[data-testid="tweet"]');

    tweets.forEach(tweet => {
      // Check if tweet has video
      if (this.hasVideo(tweet) && !this.isProcessed(tweet)) {
        this.injectArchiveButton(tweet);
      }
    });
  }

  hasVideo(tweetElement) {
    return tweetElement.querySelector('video') !== null ||
           tweetElement.querySelector('[data-testid="videoPlayer"]') !== null;
  }

  isProcessed(tweetElement) {
    const tweetId = this.getTweetId(tweetElement);
    return tweetId && this.processedTweets.has(tweetId);
  }

  getTweetId(tweetElement) {
    const extractor = window.XGrabbaExtractor;
    const url = extractor.getTweetUrl(tweetElement);
    return extractor.extractTweetId(url);
  }

  injectArchiveButton(tweetElement) {
    const tweetId = this.getTweetId(tweetElement);
    if (!tweetId) return;

    this.processedTweets.add(tweetId);

    // Find the action bar (like, retweet, reply buttons)
    const actionBar = tweetElement.querySelector('[role="group"]');
    if (!actionBar) return;

    // Create archive button container
    const container = document.createElement('div');
    container.className = 'xgrabba-btn-container';
    container.setAttribute('data-tweet-id', tweetId);

    // Create the button
    const button = document.createElement('button');
    button.className = 'xgrabba-archive-btn';
    button.setAttribute('aria-label', 'Archive video');
    button.setAttribute('title', 'Archive video');
    button.innerHTML = this.getIdleIcon();

    // Add click handler
    button.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      this.handleArchiveClick(tweetElement, button, tweetId);
    });

    container.appendChild(button);
    actionBar.appendChild(container);
  }

  async handleArchiveClick(tweetElement, button, tweetId) {
    // Check current state
    const currentState = this.archiveStates.get(tweetId) || 'idle';

    if (currentState === 'saving') {
      return; // Already in progress
    }

    // Extract tweet data
    const extractor = window.XGrabbaExtractor;
    const data = extractor.extractFromTweet(tweetElement);

    if (!data || data.mediaUrls.length === 0) {
      this.showToast('Could not extract video data. Try scrolling to load the video first.', 'error');
      return;
    }

    // Update state to saving
    this.setState(tweetId, button, 'saving');

    try {
      // Send to background script
      const response = await chrome.runtime.sendMessage({
        type: 'ARCHIVE_VIDEO',
        payload: data
      });

      if (response.success) {
        this.setState(tweetId, button, 'success');
        this.showToast('Video archived successfully', 'success');

        // Reset to idle after delay
        setTimeout(() => {
          this.setState(tweetId, button, 'archived');
        }, 3000);
      } else {
        this.setState(tweetId, button, 'failed');
        this.showToast(response.error || 'Archive failed', 'error');
      }
    } catch (error) {
      console.error('XGrabba: Archive error', error);
      this.setState(tweetId, button, 'failed');
      this.showToast('Archive failed: ' + error.message, 'error');
    }
  }

  setState(tweetId, button, state) {
    this.archiveStates.set(tweetId, state);

    button.className = `xgrabba-archive-btn xgrabba-state-${state}`;
    button.disabled = state === 'saving';

    switch (state) {
      case 'idle':
        button.innerHTML = this.getIdleIcon();
        button.setAttribute('title', 'Archive video');
        break;
      case 'saving':
        button.innerHTML = this.getSavingIcon();
        button.setAttribute('title', 'Archiving...');
        break;
      case 'success':
        button.innerHTML = this.getSuccessIcon();
        button.setAttribute('title', 'Archived successfully');
        break;
      case 'failed':
        button.innerHTML = this.getFailedIcon();
        button.setAttribute('title', 'Archive failed - click to retry');
        break;
      case 'archived':
        button.innerHTML = this.getArchivedIcon();
        button.setAttribute('title', 'Already archived - click to re-archive');
        break;
    }
  }

  showToast(message, type) {
    // Remove existing toast
    const existing = document.querySelector('.xgrabba-toast');
    if (existing) {
      existing.remove();
    }

    const toast = document.createElement('div');
    toast.className = `xgrabba-toast xgrabba-toast-${type}`;
    toast.textContent = message;

    document.body.appendChild(toast);

    // Auto-remove after 3 seconds
    setTimeout(() => {
      toast.classList.add('xgrabba-toast-fade');
      setTimeout(() => toast.remove(), 300);
    }, 3000);
  }

  archiveVisibleVideo() {
    // Find the most visible tweet with video
    const tweets = document.querySelectorAll('article[data-testid="tweet"]');

    for (const tweet of tweets) {
      if (this.hasVideo(tweet) && this.isInViewport(tweet)) {
        const tweetId = this.getTweetId(tweet);
        const button = document.querySelector(`.xgrabba-btn-container[data-tweet-id="${tweetId}"] button`);
        if (button) {
          this.handleArchiveClick(tweet, button, tweetId);
        }
        break;
      }
    }
  }

  isInViewport(element) {
    const rect = element.getBoundingClientRect();
    return (
      rect.top >= 0 &&
      rect.top <= (window.innerHeight || document.documentElement.clientHeight) * 0.5
    );
  }

  // SVG Icons
  getIdleIcon() {
    return `<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor">
      <path d="M12 3v10.586l3.293-3.293 1.414 1.414L12 16.414l-4.707-4.707 1.414-1.414L12 13.586V3h2z"/>
      <path d="M5 17v4h14v-4h2v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4h2z"/>
    </svg>`;
  }

  getSavingIcon() {
    return `<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" class="xgrabba-spin">
      <path d="M12 2a10 10 0 1 0 10 10A10 10 0 0 0 12 2zm0 18a8 8 0 1 1 8-8 8 8 0 0 1-8 8z" opacity="0.3"/>
      <path d="M12 4V2a10 10 0 0 1 10 10h-2a8 8 0 0 0-8-8z"/>
    </svg>`;
  }

  getSuccessIcon() {
    return `<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor">
      <path d="M9 16.17L4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41L9 16.17z"/>
    </svg>`;
  }

  getFailedIcon() {
    return `<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor">
      <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 15h-2v-2h2v2zm0-4h-2V7h2v6z"/>
    </svg>`;
  }

  getArchivedIcon() {
    return `<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor">
      <path d="M12 3v10.586l3.293-3.293 1.414 1.414L12 16.414l-4.707-4.707 1.414-1.414L12 13.586V3h2z"/>
      <path d="M5 17v4h14v-4h2v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4h2z"/>
      <circle cx="18" cy="6" r="4" fill="#00BA7C"/>
    </svg>`;
  }
}

// Initialize injector
window.XGrabbaInjector = new TweetInjector();
