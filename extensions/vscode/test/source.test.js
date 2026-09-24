'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const sourceDir = path.resolve(__dirname, '../src');

function sourceFiles(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => {
    const name = path.join(dir, entry.name);
    if (entry.isDirectory()) return sourceFiles(name);
    return entry.isFile() && entry.name.endsWith('.js') ? [name] : [];
  });
}

function controlCharacter(text, file) {
  let line = 1;
  for (const char of text) {
    const code = char.codePointAt(0);
    if ((code < 0x20 && code !== 0x09 && code !== 0x0a && code !== 0x0d) || code === 0x7f) {
      return `${file}:${line}: raw control character U+${code.toString(16).toUpperCase().padStart(4, '0')}`;
    }
    if (code === 0x0a) line++;
  }
  return undefined;
}

test('source files contain no raw control characters', () => {
  const files = sourceFiles(sourceDir);
  assert.ok(files.length > 0, `no JavaScript source files found in ${sourceDir}`);
  for (const file of files) {
    const relative = path.relative(sourceDir, file);
    assert.equal(controlCharacter(fs.readFileSync(file, 'utf8'), relative), undefined);
  }
});

test('source check reports the file, line and code point', () => {
  assert.throws(() => assert.equal(
    controlCharacter('first\nsecond\u0000', 'nested/example.js'), undefined),
  /nested\/example\.js:2: raw control character U\+0000/);
});
