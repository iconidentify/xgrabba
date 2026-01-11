// XGrabba Content Script - Data Extractor

class TweetExtractor {
  constructor() {
    this.videoDataCache = new Map();
    this.setupNetworkInterceptor();
  }

  // Extract data from a tweet element
  extractFromTweet(tweetElement) {
    try {
      const tweetUrl = this.getTweetUrl(tweetElement);
      const tweetId = this.extractTweetId(tweetUrl);

      if (!tweetId) {
        console.warn('XGrabba: Could not extract tweet ID');
        return null;
      }

      // Check cache for video data from network intercept
      const cachedData = this.videoDataCache.get(tweetId);

      const data = {
        tweetUrl,
        tweetId,
        authorUsername: this.getAuthorUsername(tweetElement),
        authorName: this.getAuthorName(tweetElement),
        tweetText: this.getTweetText(tweetElement),
        postedAt: this.getPostedAt(tweetElement),
        mediaUrls: cachedData?.mediaUrls || this.getVideoUrls(tweetElement),
        duration: cachedData?.duration || 0,
        resolution: cachedData?.resolution || ''
      };

      return data;
    } catch (error) {
      console.error('XGrabba: Extraction error', error);
      return null;
    }
  }

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

  extractTweetId(url) {
    const match = url.match(/\/status\/(\d+)/);
    return match ? match[1] : null;
  }

  getAuthorUsername(tweetElement) {
    // Look for the username in the tweet header
    const usernameEl = tweetElement.querySelector('[data-testid="User-Name"] a[href^="/"]');
    if (usernameEl) {
      const href = usernameEl.getAttribute('href');
      return href.replace('/', '').split('/')[0];
    }

    // Fallback: extract from URL
    const tweetUrl = this.getTweetUrl(tweetElement);
    const match = tweetUrl.match(/x\.com\/(\w+)\/status/);
    return match ? match[1] : 'unknown';
  }

  getAuthorName(tweetElement) {
    const nameEl = tweetElement.querySelector('[data-testid="User-Name"] span');
    return nameEl?.textContent?.trim() || '';
  }

  getTweetText(tweetElement) {
    const textEl = tweetElement.querySelector('[data-testid="tweetText"]');
    return textEl?.textContent?.trim() || '';
  }

  getPostedAt(tweetElement) {
    const timeEl = tweetElement.querySelector('time');
    if (timeEl) {
      const datetime = timeEl.getAttribute('datetime');
      if (datetime) {
        return datetime;
      }
    }
    return new Date().toISOString();
  }

  getVideoUrls(tweetElement) {
    const urls = [];

    // Look for video elements
    const videoEl = tweetElement.querySelector('video');
    if (videoEl) {
      const src = videoEl.getAttribute('src');
      if (src && src.includes('video.twimg.com')) {
        urls.push(src);
      }

      // Check source elements
      const sources = videoEl.querySelectorAll('source');
      sources.forEach(source => {
        const srcUrl = source.getAttribute('src');
        if (srcUrl && srcUrl.includes('video.twimg.com')) {
          urls.push(srcUrl);
        }
      });
    }

    // Look for blob URLs and try to find original
    // This is limited - real URLs come from network intercept

    return urls;
  }

  // Setup network request interceptor to capture video URLs
  setupNetworkInterceptor() {
    // Override fetch to capture video data from API responses
    const originalFetch = window.fetch;
    const self = this;

    window.fetch = async function(...args) {
      const response = await originalFetch.apply(this, args);

      // Check if this is a tweet detail request
      const url = args[0]?.toString() || args[0];
      if (url.includes('TweetDetail') || url.includes('TweetResultByRestId')) {
        try {
          const clone = response.clone();
          const data = await clone.json();
          self.extractVideoDataFromResponse(data);
        } catch (e) {
          // Ignore parsing errors
        }
      }

      return response;
    };
  }

  extractVideoDataFromResponse(data) {
    try {
      // Navigate through the GraphQL response structure
      const instructions = data?.data?.tweetResult?.result ||
                          data?.data?.threaded_conversation_with_injections_v2?.instructions;

      if (!instructions) return;

      // Find tweet entries
      const entries = Array.isArray(instructions)
        ? instructions.flatMap(i => i.entries || [])
        : [];

      entries.forEach(entry => {
        this.processEntry(entry);
      });

      // Also check direct result
      if (data?.data?.tweetResult?.result) {
        this.processTweetResult(data.data.tweetResult.result);
      }
    } catch (error) {
      console.debug('XGrabba: Could not parse response', error);
    }
  }

  processEntry(entry) {
    const content = entry?.content?.itemContent?.tweet_results?.result;
    if (content) {
      this.processTweetResult(content);
    }
  }

  processTweetResult(result) {
    const legacy = result?.legacy || result?.tweet?.legacy;
    if (!legacy) return;

    const tweetId = legacy.id_str || result.rest_id;
    if (!tweetId) return;

    const extendedEntities = legacy.extended_entities;
    if (!extendedEntities?.media) return;

    extendedEntities.media.forEach(media => {
      if (media.type === 'video' || media.type === 'animated_gif') {
        const videoInfo = media.video_info;
        if (videoInfo?.variants) {
          // Sort by bitrate to get highest quality first
          const sortedVariants = videoInfo.variants
            .filter(v => v.content_type === 'video/mp4')
            .sort((a, b) => (b.bitrate || 0) - (a.bitrate || 0));

          if (sortedVariants.length > 0) {
            const best = sortedVariants[0];
            this.videoDataCache.set(tweetId, {
              mediaUrls: sortedVariants.map(v => v.url),
              duration: Math.floor((videoInfo.duration_millis || 0) / 1000),
              resolution: this.extractResolution(best.url)
            });
          }
        }
      }
    });
  }

  extractResolution(url) {
    // Extract resolution from URL like /vid/1280x720/
    const match = url.match(/\/vid\/(\d+x\d+)\//);
    return match ? match[1] : '';
  }

  // Get cached video data for a tweet
  getCachedVideoData(tweetId) {
    return this.videoDataCache.get(tweetId);
  }
}

// Export for use by injector
window.XGrabbaExtractor = new TweetExtractor();
