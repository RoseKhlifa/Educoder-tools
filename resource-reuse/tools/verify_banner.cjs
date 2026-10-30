// Exercise the actual original/patched inline hook with online and offline
// failures. The DOM stub records insertion; it does not hide the banner by CSS.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const filename = process.argv[2];
const mode = process.argv[3];
if (!filename || !['baseline', 'modified'].includes(mode)) {
  throw new Error('Usage: node verify_banner.cjs HTML_FILE baseline|modified');
}
const html = fs.readFileSync(filename, 'utf8');
const script = Array.from(html.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/g), match => match[1])
  .find(source => source.includes('window.showNetworkErrorTip'));
assert.ok(script, 'Original hook script is present');
const inserted = [];
const context = {
  window: {addEventListener() {}}, navigator: {onLine: false},
  document: {
    getElementById() {return null;},
    createElement() {return {};},
    body: {prepend(element) {inserted.push(element);}},
  },
};
vm.runInNewContext(script, context);
context.window.showNetworkErrorTip('offline verification');
context.navigator.onLine = true;
context.window.showNetworkErrorTip('online request failure');
if (mode === 'baseline') {
  assert.equal(inserted.filter(element => element.id === 'showNetworkError').length, 2);
  console.log('BASELINE_BANNER=VISIBLE; INSERTIONS=2');
} else {
  assert.equal(inserted.length, 0);
  console.log('MODIFIED_BANNER=REMOVED; INSERTIONS=0; ONLINE_AND_OFFLINE=PASS');
}
