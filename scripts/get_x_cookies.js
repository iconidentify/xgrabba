// Run this in Chrome DevTools console while on x.com
// Copy the output and paste it to set environment variables

const cookies = document.cookie.split(';').reduce((acc, c) => {
  const [key, val] = c.trim().split('=');
  acc[key] = val;
  return acc;
}, {});

const authToken = cookies['auth_token'] || 'NOT_FOUND';
const ct0 = cookies['ct0'] || 'NOT_FOUND';

console.log('=== Copy these commands ===');
console.log(`export X_AUTH_TOKEN="${authToken}"`);
console.log(`export X_CT0="${ct0}"`);
console.log('');
console.log('=== Then run ===');
console.log('go test -v -run TestUsersByRestIds_Integration ./pkg/twitter/');
