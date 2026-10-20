// The desktop page's view math and touch recognizer (spec 66, D8), under
// node with no dependencies. desktop_page_test.dart runs this as part of
// `flutter test`; on its own: node test/desktop_page_test.js
'use strict';

const assert = require('node:assert/strict');
const path = require('node:path');
const page = require(path.join(__dirname, '../../internal/desktop/web/desktop.js'));

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const near = (a, b, msg) => assert.ok(Math.abs(a - b) < 1e-6, `${msg}: ${a} != ${b}`);

// A phone in portrait, 412 wide and 700 tall, on a 1280×800 desktop.
const VW = 412, VH = 700, RW = 1280, RH = 800;

test('the view starts at fit-to-width, letterboxed in portrait', () => {
  const v = page.fitView(VW, VH, RW, RH);
  near(v.zoom, 1, 'zoom');
  near(v.x, 0, 'x');
  const height = RH * (VW / RW);
  near(v.y, (VH - height) / 2, 'centred vertically');
});

test('in landscape the screen is taller than the phone and starts at the top', () => {
  const v = page.fitView(915, 300, RW, RH);
  near(v.y, 0, 'top edge');
  assert.ok(page.minZoom(915, 300, RW, RH) < 1, 'can zoom out to the whole screen');
});

test('a screen point and its remote pixel map both ways', () => {
  const v = page.fitView(VW, VH, RW, RH);
  const r = page.toRemote(v, VW, RW, RH, 206, v.y + 128.75);
  assert.deepEqual(r, { x: 640, y: 400 });
  const s = page.toScreen(v, VW, RW, 640, 400);
  near(s.x, 206, 'x');
  near(s.y, v.y + 128.75, 'y');
});

test('a point off the screen clicks its nearest edge', () => {
  const v = page.fitView(VW, VH, RW, RH);
  assert.deepEqual(page.toRemote(v, VW, RW, RH, -50, 0), { x: 0, y: 0 });
  assert.deepEqual(page.toRemote(v, VW, RW, RH, 9999, 9999), { x: RW - 1, y: RH - 1 });
});

test('a pinch keeps the point under the fingers where it was', () => {
  // Short enough that the zoomed screen overflows and is not re-centred.
  const vh = 300;
  const start = page.fitView(VW, vh, RW, RH);
  const c = { x: 206, y: 150 };
  const before = page.toRemote(start, VW, RW, RH, c.x, c.y);
  const v = page.pinchView(start, VW, vh, RW, RH, 100, c.x, c.y, 200, c.x, c.y);
  near(v.zoom, 2, 'zoom doubles');
  const after = page.toRemote(v, VW, RW, RH, c.x, c.y);
  assert.deepEqual(after, before);
});

test('a pinch that moves pans with the fingers', () => {
  const start = page.pinchView(page.fitView(VW, VH, RW, RH), VW, VH, RW, RH, 100, 200, 300, 300, 200, 300);
  const v = page.pinchView(start, VW, VH, RW, RH, 100, 200, 300, 100, 150, 300);
  near(v.x, start.x - 50, 'moved left with the fingers');
});

test('zoom is held between the whole screen and four times', () => {
  const start = page.fitView(VW, VH, RW, RH);
  near(page.pinchView(start, VW, VH, RW, RH, 10, 0, 0, 1000, 0, 0).zoom, page.MAX_ZOOM, 'max');
  near(page.pinchView(start, VW, VH, RW, RH, 1000, 0, 0, 10, 0, 0).zoom, page.minZoom(VW, VH, RW, RH), 'min');
});

test('a pan cannot pull an edge inside the phone', () => {
  const zoomed = page.clampView({ zoom: 2, x: 0, y: 0 }, VW, VH, RW, RH);
  const v = page.panView(zoomed, VW, VH, RW, RH, 500, 0);
  near(v.x, 0, 'left edge stays put');
  const w = page.panView(zoomed, VW, VH, RW, RH, -99999, 0);
  near(w.x, VW - RW * (VW / RW) * 2, 'right edge stays put');
});

test('reveal moves the view only as far as it takes', () => {
  const zoomed = page.clampView({ zoom: 3, x: 0, y: -100 }, VW, 300, RW, RH);
  const v = page.revealView(zoomed, VW, 300, RW, RH, 200, 320, 24);
  near(v.y, zoomed.y - 44, 'scrolled up by the overshoot');
  near(v.x, zoomed.x, 'x untouched');
});

test('wheel notches carry the remainder, fingers up scroll down', () => {
  let w = page.wheelSteps(0, -50, 36);
  assert.equal(w.down, 1);
  assert.equal(w.up, 0);
  near(w.carry, -14, 'carry');
  w = page.wheelSteps(w.carry, -30, 36);
  assert.equal(w.down, 1);
  w = page.wheelSteps(0, 80, 36);
  assert.equal(w.up, 2);
});

// ── The recognizer, on a clock the test turns ──

function harness() {
  const events = [];
  let now = 0;
  let timers = [];
  let nextId = 1;
  const record = (name) => (...args) => events.push([name, ...args]);
  const r = new page.Recognizer(
    {
      click: record('click'),
      right: record('right'),
      doubleClick: record('double'),
      press: record('press'),
      move: record('move'),
      release: record('release'),
      wheel: record('wheel'),
      pan: record('pan'),
      pinchStart: record('pinchStart'),
      pinch: record('pinch'),
    },
    {
      set: (fn, ms) => { const id = nextId++; timers.push({ id, at: now + ms, fn }); return id; },
      clear: (id) => { timers = timers.filter((t) => t.id !== id); },
    }
  );
  const advance = (ms) => {
    now += ms;
    const due = timers.filter((t) => t.at <= now);
    timers = timers.filter((t) => t.at > now);
    due.forEach((t) => t.fn());
  };
  return { r, events, advance, names: () => events.map((e) => e[0]) };
}

const P = (x, y) => ({ x, y });

test('a tap clicks once the double-tap window has passed', () => {
  const h = harness();
  h.r.down([P(10, 10)]);
  h.r.up(0);
  assert.deepEqual(h.names(), []);
  h.advance(page.TOUCH.doubleTapMs);
  assert.deepEqual(h.events, [['click', 0, P(10, 10)]]);
});

test('a long press is a right click and nothing more', () => {
  const h = harness();
  h.r.down([P(10, 10)]);
  h.advance(page.TOUCH.longPressMs);
  h.r.up(0);
  h.advance(1000);
  assert.deepEqual(h.names(), ['right']);
});

test('two taps are a double click', () => {
  const h = harness();
  h.r.down([P(10, 10)]);
  h.r.up(0);
  h.advance(100);
  h.r.down([P(14, 12)]);
  h.r.up(0);
  h.advance(1000);
  assert.deepEqual(h.events, [['double', P(10, 10)]]);
});

test('a tap then a drag holds the button from the tap to the lift', () => {
  const h = harness();
  h.r.down([P(10, 10)]);
  h.r.up(0);
  h.advance(100);
  h.r.down([P(10, 10)]);
  h.r.move([P(60, 10)]);
  h.r.move([P(90, 40)]);
  h.r.up(0);
  assert.deepEqual(h.names(), ['press', 'move', 'move', 'release']);
  assert.deepEqual(h.events[0], ['press', P(10, 10)]);
  assert.deepEqual(h.events[3], ['release', P(90, 40)], 'lets go where the finger lifted');
});

test('one finger dragging pans the view', () => {
  const h = harness();
  h.r.down([P(100, 100)]);
  h.r.move([P(130, 100)]);
  h.r.move([P(150, 90)]);
  h.r.up(0);
  h.advance(1000);
  assert.deepEqual(h.events, [['pan', 30, 0], ['pan', 20, -10]]);
});

test('two fingers moving together turn the wheel', () => {
  const h = harness();
  h.r.down([P(100, 300)]);
  h.r.down([P(100, 300), P(200, 300)]);
  h.r.move([P(100, 200), P(200, 200)]);
  h.r.up(1);
  h.r.up(0);
  h.advance(1000);
  assert.deepEqual(h.events, [['wheel', 0, 2]]);
});

test('two fingers spreading zoom, and never click', () => {
  const h = harness();
  h.r.down([P(100, 300)]);
  h.r.down([P(100, 300), P(200, 300)]);
  h.r.move([P(50, 300), P(250, 300)]);
  h.r.up(1);
  h.r.up(0);
  h.advance(1000);
  assert.deepEqual(h.names(), ['pinchStart', 'pinch']);
  assert.deepEqual(h.events[1], ['pinch', 100, P(150, 300), 200, P(150, 300)]);
});

test('a tap somewhere else lets the first tap click on its own', () => {
  const h = harness();
  h.r.down([P(10, 10)]);
  h.r.up(0);
  h.advance(50);
  h.r.down([P(300, 300)]);
  h.r.up(0);
  h.advance(1000);
  assert.deepEqual(h.events, [['click', 0, P(10, 10)], ['click', 0, P(300, 300)]]);
});

let failed = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (e) {
    failed++;
    console.log(`not ok - ${name}\n  ${e.message}`);
  }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
