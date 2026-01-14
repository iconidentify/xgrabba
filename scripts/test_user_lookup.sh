#!/bin/bash
# Test script for UserByRestId lookup
#
# To get credentials from your browser:
# 1. Open x.com in Chrome
# 2. Open DevTools (F12) -> Application -> Cookies -> https://x.com
# 3. Copy values for: auth_token, ct0
# 4. Or copy ALL cookies as a string
#
# Usage:
#   X_AUTH_TOKEN=xxx X_CT0=yyy ./scripts/test_user_lookup.sh
#
# Or with full cookies:
#   X_COOKIES="auth_token=xxx; ct0=yyy; ..." ./scripts/test_user_lookup.sh

set -e

USER_ID="${USER_ID:-1685114627024121856}"  # @psyop4921

if [ -z "$X_AUTH_TOKEN" ] || [ -z "$X_CT0" ]; then
    if [ -z "$X_COOKIES" ]; then
        echo "Error: Set X_AUTH_TOKEN and X_CT0, or X_COOKIES"
        echo ""
        echo "Get from browser DevTools -> Application -> Cookies -> x.com"
        exit 1
    fi
    COOKIE_STRING="$X_COOKIES"
else
    COOKIE_STRING="auth_token=$X_AUTH_TOKEN; ct0=$X_CT0"
fi

BEARER="AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs=1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"

FEATURES='{"hidden_profile_subscriptions_enabled":true,"rweb_tipjar_consumption_enabled":true,"responsive_web_graphql_exclude_directive_enabled":true,"verified_phone_label_enabled":false,"subscriptions_verification_info_is_identity_verified_enabled":true,"subscriptions_verification_info_verified_since_enabled":true,"highlights_tweets_tab_ui_enabled":true,"responsive_web_twitter_article_notes_tab_enabled":true,"subscriptions_feature_can_gift_premium":true,"creator_subscriptions_tweet_preview_api_enabled":true,"responsive_web_graphql_skip_user_profile_image_extensions_enabled":false,"responsive_web_graphql_timeline_navigation_enabled":true}'

VARIABLES="{\"userId\":\"$USER_ID\",\"withSafetyModeUserFields\":true}"
QUERY_ID="xf3jd90KKBCUxdlI_tNHZw"

# URL encode
ENCODED_VARS=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$VARIABLES'))")
ENCODED_FEATURES=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$FEATURES'))")

URL="https://x.com/i/api/graphql/$QUERY_ID/UserByRestId?variables=$ENCODED_VARS&features=$ENCODED_FEATURES"

echo "Testing UserByRestId for user: $USER_ID"
echo "Cookie length: ${#COOKIE_STRING}"
echo ""

RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer $BEARER" \
    -H "x-csrf-token: $X_CT0" \
    -H "Cookie: $COOKIE_STRING" \
    -H "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36" \
    -H "Content-Type: application/json" \
    -H "x-twitter-active-user: yes" \
    -H "x-twitter-client-language: en" \
    -H "x-twitter-auth-type: OAuth2Session" \
    -H "Accept: */*" \
    -H "Accept-Language: en-US,en;q=0.9" \
    -H "Accept-Encoding: gzip, deflate, br" \
    -H "Origin: https://x.com" \
    -H "Referer: https://x.com/" \
    -H "Sec-Fetch-Dest: empty" \
    -H "Sec-Fetch-Mode: cors" \
    -H "Sec-Fetch-Site: same-origin" \
    -H 'Sec-Ch-Ua: "Chromium";v="122", "Not(A:Brand";v="24", "Google Chrome";v="122"' \
    -H "Sec-Ch-Ua-Mobile: ?0" \
    -H 'Sec-Ch-Ua-Platform: "macOS"' \
    --compressed \
    "$URL")

HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | sed '$d')

echo "HTTP Status: $HTTP_CODE"
echo ""

if [ "$HTTP_CODE" = "200" ]; then
    echo "SUCCESS! Response:"
    echo "$BODY" | jq -r '.data.user.result | "  screen_name: \(.legacy.screen_name)\n  name: \(.legacy.name)"' 2>/dev/null || echo "$BODY"
else
    echo "FAILED!"
    if echo "$BODY" | grep -q "blocked"; then
        echo ">>> CLOUDFLARE BLOCKED THE REQUEST <<<"
    fi
    echo "$BODY" | head -c 500
fi
