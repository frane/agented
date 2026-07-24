#!/usr/bin/env node
// npx shim for agented (`ae`) — a text editor for LLMs, not humans.
//
// Downloads the platform binary from the GitHub release matching this
// package's version, verifies its sha256 against the release's
// checksums.txt, caches it under the user cache dir, and execs it with
// all arguments passed through. Zero npm dependencies; extraction uses
// the system `tar` (present on macOS, Linux, and Windows 10+, where
// bsdtar also reads .zip).
//
// Cache layout: <cache>/agented/v<version>/ae[.exe]
//   <cache> = $AGENTED_CACHE_DIR || $XDG_CACHE_HOME || %LOCALAPPDATA% || ~/.cache
// Re-runs never touch the network once the binary is cached.

'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const https = require('https');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const pkg = require('../package.json');
const VERSION = pkg.version;
const REPO = 'frane/agented';

function fail(msg) {
  process.stderr.write(`agented (npx shim): ${msg}\n`);
  process.exit(1);
}

function assetName() {
  const osName = { darwin: 'darwin', linux: 'linux', win32: 'windows' }[process.platform];
  const archName = { x64: 'x86_64', arm64: 'arm64' }[process.arch];
  if (!osName || !archName) {
    fail(`unsupported platform ${process.platform}/${process.arch}; ` +
      `install from https://github.com/${REPO}/releases or \`brew install frane/tap/agented\``);
  }
  if (osName === 'windows' && archName === 'arm64') {
    fail('windows/arm64 builds are not published; use WSL or x64 emulation');
  }
  const ext = osName === 'windows' ? 'zip' : 'tar.gz';
  return `agented_${VERSION}_${osName}_${archName}.${ext}`;
}

function cacheRoot() {
  if (process.env.AGENTED_CACHE_DIR) return process.env.AGENTED_CACHE_DIR;
  if (process.env.XDG_CACHE_HOME) return path.join(process.env.XDG_CACHE_HOME, 'agented');
  if (process.platform === 'win32' && process.env.LOCALAPPDATA) {
    return path.join(process.env.LOCALAPPDATA, 'agented', 'cache');
  }
  return path.join(os.homedir(), '.cache', 'agented');
}

function download(url, dest, redirects) {
  return new Promise((resolve, reject) => {
    if (redirects > 5) return reject(new Error(`too many redirects for ${url}`));
    https.get(url, { headers: { 'user-agent': `agented-npx/${VERSION}` } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume();
        return resolve(download(res.headers.location, dest, redirects + 1));
      }
      if (res.statusCode !== 200) {
        res.resume();
        return reject(new Error(`GET ${url}: HTTP ${res.statusCode}`));
      }
      const out = fs.createWriteStream(dest);
      res.pipe(out);
      out.on('finish', () => out.close(resolve));
      out.on('error', reject);
      res.on('error', reject);
    }).on('error', reject);
  });
}

function sha256(file) {
  return crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
}

async function ensureBinary() {
  const binName = process.platform === 'win32' ? 'ae.exe' : 'ae';
  const dir = path.join(cacheRoot(), `v${VERSION}`);
  const bin = path.join(dir, binName);
  if (fs.existsSync(bin)) return bin;

  const asset = assetName();
  const base = `https://github.com/${REPO}/releases/download/v${VERSION}`;
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'agented-'));
  const archive = path.join(tmp, asset);
  const sums = path.join(tmp, 'checksums.txt');

  process.stderr.write(`agented: fetching ae v${VERSION} (${asset}) — one-time download, cached in ${dir}\n`);
  try {
    await download(`${base}/${asset}`, archive, 0);
    await download(`${base}/checksums.txt`, sums, 0);

    const line = fs.readFileSync(sums, 'utf8').split('\n').find((l) => l.trim().endsWith(asset));
    if (!line) throw new Error(`${asset} not listed in checksums.txt`);
    const want = line.trim().split(/\s+/)[0];
    const got = sha256(archive);
    if (want !== got) throw new Error(`sha256 mismatch for ${asset}: want ${want}, got ${got}`);

    const unpack = path.join(tmp, 'unpack');
    fs.mkdirSync(unpack);
    const tar = spawnSync('tar', ['-xf', archive, '-C', unpack], { stdio: ['ignore', 'ignore', 'inherit'] });
    if (tar.status !== 0) throw new Error(`tar extraction failed (status ${tar.status})`);

    const extracted = path.join(unpack, binName);
    if (!fs.existsSync(extracted)) throw new Error(`${binName} not found in ${asset}`);
    if (process.platform !== 'win32') fs.chmodSync(extracted, 0o755);

    fs.mkdirSync(dir, { recursive: true });
    // Atomic-ish install: rename into place; if a parallel run won, use its copy.
    try {
      fs.renameSync(extracted, bin);
    } catch (e) {
      if (!fs.existsSync(bin)) throw e;
    }
    return bin;
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

ensureBinary()
  .then((bin) => {
    const res = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
    if (res.error) fail(res.error.message);
    process.exit(res.status === null ? 1 : res.status);
  })
  .catch((err) => fail(err.message));
