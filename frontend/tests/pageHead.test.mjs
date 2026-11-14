import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

const page = await readFile(new URL('../index.html', import.meta.url), 'utf8')

test('the console keeps its own theme and tells Dark Reader to leave it alone', () => {
  assert.match(page, /<meta name="color-scheme" content="light dark"\s*\/?>/)
  assert.match(page, /<meta name="darkreader-lock"\s*\/?>/)
})
