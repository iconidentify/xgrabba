// XGrabba Content Script - Simplified Data Extractor
// The backend now handles fetching all tweet data - we just need the URL

class TweetExtractor {
  constructor() {
    // No need for video caching anymore - backend fetches everything
  }

  // Get the tweet URL from a tweet element
  getTweetUrl(tweetElement) {
    // Look for the timestamp link which contains the tweet URL
    const timeLink = tweetElement.querySelector('a[href*="/status/"] time')?.closest('a');
    if (timeLink) {
      return 'https://x.com' + timeLink.getAttribute('href');
    }

    // Fallback: look for any status link
    const statusLink = tweetElement.querySelector('a[href*="/status/"]');
    if (statusLink) {
      const href = statusLink.getAttribute('href');
      if (href.startsWith('/')) {
        return 'https://x.com' + href;
      }
      return href;
    }

    return window.location.href;
  }

  // Extract tweet ID from URL
  extractTweetId(url) {
    const match = url.match(/\/status\/(\d+)/);
    return match ? match[1] : null;
  }
}

// Export for use by injector
window.XGrabbaExtractor = new TweetExtractor();
