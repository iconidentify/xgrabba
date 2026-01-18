// XGrabba Content Script - Hardened Data Extractor
// Robust tweet URL and ID extraction with multiple fallback strategies

class TweetExtractor {
  constructor() {
    // Cache extracted URLs to avoid repeated DOM queries
    this.urlCache = new WeakMap();
  }

  // Get the tweet URL from a tweet element using multiple strategies
  getTweetUrl(tweetElement) {
    if (!tweetElement) return null;

    // Check cache first
    if (this.urlCache.has(tweetElement)) {
      return this.urlCache.get(tweetElement);
    }

    const url = this.extractUrl(tweetElement);
    if (url) {
      this.urlCache.set(tweetElement, url);
    }
    return url;
  }

  extractUrl(tweetElement) {
    // Strategy 1: Timestamp link (most reliable)
    const timeLink = this.findTimestampLink(tweetElement);
    if (timeLink) {
      return this.normalizeUrl(timeLink);
    }

    // Strategy 2: Status links in the tweet
    const statusLink = this.findStatusLink(tweetElement);
    if (statusLink) {
      return this.normalizeUrl(statusLink);
    }

    // Strategy 3: Data attributes
    const dataUrl = this.findDataAttributeUrl(tweetElement);
    if (dataUrl) {
      return this.normalizeUrl(dataUrl);
    }

    // Strategy 4: Use current URL if on a tweet page (ONLY if it matches the tweet's ID)
    // This is risky - only use for single tweet views
    const currentUrl = this.getCurrentPageUrl();
    if (currentUrl && this.isStatusPage(currentUrl)) {
      // Verify this is actually the right tweet by checking for other indicators
      if (this.isSingleTweetView()) {
        return currentUrl;
      }
    }

    // No URL found
    console.warn('[XGrabba] Could not extract tweet URL from element');
    return null;
  }

  findTimestampLink(tweetElement) {
    // Look for time element inside a status link
    const timeElement = tweetElement.querySelector('a[href*="/status/"] time');
    if (timeElement) {
      const link = timeElement.closest('a');
      if (link) {
        return link.getAttribute('href');
      }
    }

    // Fallback: look for any link containing time
    const allTimeElements = tweetElement.querySelectorAll('time');
    for (const time of allTimeElements) {
      const link = time.closest('a[href*="/status/"]');
      if (link) {
        return link.getAttribute('href');
      }
    }

    return null;
  }

  findStatusLink(tweetElement) {
    // Look for status links, preferring ones that aren't quote tweets or replies
    const allStatusLinks = tweetElement.querySelectorAll('a[href*="/status/"]');

    // Filter and score links
    let bestLink = null;
    let bestScore = -1;

    for (const link of allStatusLinks) {
      const href = link.getAttribute('href');
      if (!href || !href.includes('/status/')) continue;

      let score = 0;

      // Prefer links that contain time (timestamp links)
      if (link.querySelector('time')) {
        score += 100;
      }

      // Prefer links that are direct children of the tweet
      if (link.closest('article') === tweetElement) {
        score += 50;
      }

      // Avoid quote tweet links (usually nested in a special container)
      const isQuoteTweet = link.closest('[data-testid="quoteTweet"]') !== null;
      if (isQuoteTweet) {
        score -= 200;
      }

      // Avoid reply links
      const isReplyLink = link.closest('[data-testid="reply"]') !== null;
      if (isReplyLink) {
        score -= 100;
      }

      // Prefer links with shorter paths (main tweet vs nested content)
      const pathDepth = (href.match(/\//g) || []).length;
      score -= pathDepth * 2;

      if (score > bestScore) {
        bestScore = score;
        bestLink = href;
      }
    }

    return bestLink;
  }

  findDataAttributeUrl(tweetElement) {
    // Look for data attributes that might contain the tweet ID
    const possibleAttributes = ['data-tweet-id', 'data-status-id', 'data-item-id'];

    for (const attr of possibleAttributes) {
      const element = tweetElement.querySelector(`[${attr}]`) ||
                      (tweetElement.hasAttribute(attr) ? tweetElement : null);
      if (element) {
        const id = element.getAttribute(attr);
        if (id && /^\d{10,}$/.test(id)) {
          // Construct URL from ID (use placeholder username)
          return `/x/status/${id}`;
        }
      }
    }

    return null;
  }

  getCurrentPageUrl() {
    const url = window.location.href;
    if (this.isStatusPage(url)) {
      return url;
    }
    return null;
  }

  isStatusPage(url) {
    return /\/status\/\d+/.test(url);
  }

  isSingleTweetView() {
    // Check if we're on a single tweet view (not timeline)
    // Single tweet views typically have specific URL patterns and DOM structures
    const url = window.location.pathname;
    if (!/\/status\/\d+/.test(url)) {
      return false;
    }

    // Check if there's only one main tweet article
    const articles = document.querySelectorAll('article[data-testid="tweet"]');
    if (articles.length === 1) {
      return true;
    }

    // Check for focal tweet indicator
    const focalTweet = document.querySelector('[data-testid="tweet"][tabindex="-1"]');
    return focalTweet !== null;
  }

  normalizeUrl(url) {
    if (!url) return null;

    // Handle relative URLs
    if (url.startsWith('/')) {
      return 'https://x.com' + url;
    }

    // Handle twitter.com URLs
    if (url.includes('twitter.com')) {
      return url.replace('twitter.com', 'x.com');
    }

    // Ensure https
    if (!url.startsWith('http')) {
      return 'https://x.com/' + url.replace(/^\/+/, '');
    }

    return url;
  }

  // Extract tweet ID from URL
  extractTweetId(url) {
    if (!url) return null;

    // Match /status/{id} pattern
    const match = url.match(/\/status\/(\d+)/);
    if (match && match[1]) {
      // Validate ID length (Twitter IDs are 18-19 digits)
      const id = match[1];
      if (id.length >= 15 && id.length <= 25) {
        return id;
      }
    }

    return null;
  }

  // Validate that a URL looks like a valid tweet URL
  isValidTweetUrl(url) {
    if (!url) return false;

    // Must be x.com or twitter.com
    if (!url.includes('x.com') && !url.includes('twitter.com')) {
      return false;
    }

    // Must have /status/ pattern
    if (!/\/status\/\d{15,}/.test(url)) {
      return false;
    }

    return true;
  }

  // Extract author info from a tweet element
  // Returns: { username, displayName, avatarUrl }
  getAuthorInfo(tweetElement) {
    if (!tweetElement) return null;

    try {
      const result = {
        username: null,
        displayName: null,
        avatarUrl: null
      };

      // Strategy 1: Find avatar image (most reliable for avatar URL)
      // Avatar images are typically the first img in the tweet with profile_images in src
      const avatarImg = tweetElement.querySelector('img[src*="profile_images"]');
      if (avatarImg) {
        let avatarUrl = avatarImg.src;
        // Upgrade to higher resolution: replace _normal with _400x400 or remove size suffix
        avatarUrl = avatarUrl.replace(/_normal\.(jpg|jpeg|png|gif|webp)/i, '_400x400.$1');
        avatarUrl = avatarUrl.replace(/_bigger\.(jpg|jpeg|png|gif|webp)/i, '_400x400.$1');
        result.avatarUrl = avatarUrl;
      }

      // Strategy 2: Find username from profile link
      // Look for the link that contains the author's handle (starts with @)
      const usernameLink = tweetElement.querySelector('a[href^="/"][role="link"]');
      if (usernameLink) {
        const href = usernameLink.getAttribute('href');
        if (href && href.match(/^\/[a-zA-Z0-9_]+$/)) {
          result.username = href.substring(1); // Remove leading /
        }
      }

      // Strategy 3: Find display name
      // The display name is usually near the username, in a span with specific styling
      const userCell = tweetElement.querySelector('[data-testid="User-Name"]');
      if (userCell) {
        // First span with text content is usually the display name
        const spans = userCell.querySelectorAll('span');
        for (const span of spans) {
          const text = span.textContent?.trim();
          // Skip if it looks like a username (@...)
          if (text && text.length > 0 && !text.startsWith('@')) {
            // Skip spans with only timestamps or other metadata
            if (!span.querySelector('time') && !span.querySelector('svg')) {
              result.displayName = text;
              break;
            }
          }
        }

        // Also try to get username from @handle in the cell
        for (const span of spans) {
          const text = span.textContent?.trim();
          if (text && text.startsWith('@')) {
            result.username = text.substring(1); // Remove @
            break;
          }
        }
      }

      // Validate we got at least something useful
      if (result.avatarUrl || result.username || result.displayName) {
        return result;
      }

      return null;
    } catch (error) {
      console.warn('[XGrabba] Error extracting author info:', error);
      return null;
    }
  }
}

// Export for use by injector
window.XGrabbaExtractor = new TweetExtractor();
