/*
 * The phone's desktop page (spec 66, D8).
 *
 * The Helios app loads this in a WebView and talks to it through
 * window.heliosDesktop; the page answers on the "HeliosDesktop" JavaScript
 * channel. It holds a single-use tunnel ticket, never the app's bearer token.
 *
 * The view math and the touch recognizer have no DOM in them, so
 * mobile/test/desktop_page_test.js can run them under node.
 */
(function (root) {
  'use strict';

  // ─── View math ────────────────────────────────────────────────────────────
  //
  // A view is {zoom, x, y}: how far the person has zoomed from fit-to-width,
  // and where the remote screen's top-left corner sits on the phone. Zoom 1 is
  // fit-to-width; below 1 shows the whole screen when it is taller than the
  // phone.

  var MAX_ZOOM = 4;

  function baseScale(vw, rw) {
    return rw > 0 ? vw / rw : 1;
  }

  /** The smallest zoom: the whole remote screen on the phone. */
  function minZoom(vw, vh, rw, rh) {
    if (rw <= 0 || rh <= 0) return 1;
    var whole = Math.min(vw / rw, vh / rh);
    return Math.min(1, whole / baseScale(vw, rw));
  }

  function scaleOf(view, vw, rw) {
    return baseScale(vw, rw) * view.zoom;
  }

  /**
   * Keeps the screen on the phone. A side shorter than the phone is centred,
   * which letterboxes it; a longer one may not pull its edge inside.
   */
  function clampView(view, vw, vh, rw, rh) {
    var zoom = Math.max(minZoom(vw, vh, rw, rh), Math.min(MAX_ZOOM, view.zoom));
    var s = baseScale(vw, rw) * zoom;
    var cw = rw * s;
    var ch = rh * s;
    var x = cw <= vw ? (vw - cw) / 2 : Math.min(0, Math.max(vw - cw, view.x));
    var y = ch <= vh ? (vh - ch) / 2 : Math.min(0, Math.max(vh - ch, view.y));
    return { zoom: zoom, x: x, y: y };
  }

  /** Fit-to-width, top edge first: where the view starts. */
  function fitView(vw, vh, rw, rh) {
    return clampView({ zoom: 1, x: 0, y: 0 }, vw, vh, rw, rh);
  }

  /** The remote pixel under a point on the phone, held inside the screen. */
  function toRemote(view, vw, rw, rh, sx, sy) {
    var s = scaleOf(view, vw, rw);
    return {
      x: Math.round(Math.max(0, Math.min(rw - 1, (sx - view.x) / s))),
      y: Math.round(Math.max(0, Math.min(rh - 1, (sy - view.y) / s)))
    };
  }

  function toScreen(view, vw, rw, rx, ry) {
    var s = scaleOf(view, vw, rw);
    return { x: view.x + rx * s, y: view.y + ry * s };
  }

  /**
   * A pinch from where it started: the remote point that was under the
   * fingers' centre stays under it, so the fingers zoom and pan in one move.
   */
  function pinchView(start, vw, vh, rw, rh, startDist, startCx, startCy, dist, cx, cy) {
    var s0 = scaleOf(start, vw, rw);
    var rx = (startCx - start.x) / s0;
    var ry = (startCy - start.y) / s0;
    var zoom = startDist > 0 ? start.zoom * (dist / startDist) : start.zoom;
    zoom = Math.max(minZoom(vw, vh, rw, rh), Math.min(MAX_ZOOM, zoom));
    var s = baseScale(vw, rw) * zoom;
    return clampView({ zoom: zoom, x: cx - rx * s, y: cy - ry * s }, vw, vh, rw, rh);
  }

  function panView(view, vw, vh, rw, rh, dx, dy) {
    return clampView({ zoom: view.zoom, x: view.x + dx, y: view.y + dy }, vw, vh, rw, rh);
  }

  /** Moves the view the least it takes to show a screen point. */
  function revealView(view, vw, vh, rw, rh, sx, sy, margin) {
    var dx = 0;
    var dy = 0;
    if (sx < margin) dx = margin - sx;
    else if (sx > vw - margin) dx = vw - margin - sx;
    if (sy < margin) dy = margin - sy;
    else if (sy > vh - margin) dy = vh - margin - sy;
    return panView(view, vw, vh, rw, rh, dx, dy);
  }

  /**
   * Whole wheel notches in a two-finger drag, and the remainder carried to the
   * next move. Fingers moving up pull the content up, which is what scrolling
   * down does, so a negative dy is a notch down.
   */
  function wheelSteps(carry, dy, step) {
    var total = carry + dy;
    var notches = total < 0 ? Math.ceil(total / step) : Math.floor(total / step);
    return { down: notches < 0 ? -notches : 0, up: notches > 0 ? notches : 0, carry: total - notches * step };
  }

  // ─── Touch ────────────────────────────────────────────────────────────────
  //
  // Tap: left click. Long press: right click. Two fingers apart or together:
  // zoom the view. Two fingers together in one direction: the wheel. One
  // finger after a tap: drag with the left button held. One finger otherwise:
  // pan the view.
  //
  // A tap waits a moment before it clicks, to see whether a second touch
  // turns it into a double click or a drag. Without the wait, a drag that
  // starts on a title bar is a double click first, and XFCE maximizes the
  // window instead of moving it.

  var TOUCH = {
    tapSlop: 12,
    longPressMs: 550,
    doubleTapMs: 280,
    doubleTapSlop: 32,
    twoFingerSlop: 18,
    wheelStep: 36
  };

  function dist(a, b) {
    return Math.hypot(a.x - b.x, a.y - b.y);
  }

  function centre(a, b) {
    return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
  }

  /**
   * Turns touches into actions. `out` receives:
   *   click(button, p), doubleClick(p), press(p), move(p), release(p),
   *   wheel(up, down), pan(dx, dy), pinchStart(), pinch(d0, c0, d, c), right(p)
   * where p is a point on the phone. `timers` is {set, clear}, so a test can
   * drive the clock.
   */
  function Recognizer(out, timers) {
    this.out = out;
    this.timers = timers;
    this.mode = 'idle';
    this.pendingTap = null;
  }

  Recognizer.prototype.down = function (touches) {
    var self = this;
    if (touches.length === 1 && this.mode === 'idle') {
      var p = touches[0];
      var second = this.pendingTap && dist(p, this.pendingTap.p) <= TOUCH.doubleTapSlop;
      if (this.pendingTap) {
        this.timers.clear(this.pendingTap.timer);
        // A touch somewhere else: the first tap stands on its own.
        if (!second) this.out.click(0, this.pendingTap.p);
      }
      this.one = { start: p, last: p, second: second ? this.pendingTap.p : null };
      this.pendingTap = null;
      this.mode = 'one';
      if (!second) {
        this.one.long = this.timers.set(function () {
          self.out.right(p);
          self.mode = 'spent';
        }, TOUCH.longPressMs);
      }
      return;
    }
    if (touches.length === 2 && (this.mode === 'one' || this.mode === 'pan')) {
      this.cancelOne();
      // A tap held back for a second touch that became a pinch still clicks.
      if (this.mode === 'one' && this.one.second) this.out.click(0, this.one.second);
      this.two = { d0: dist(touches[0], touches[1]), c0: centre(touches[0], touches[1]), kind: null, carry: 0 };
      this.mode = 'two';
      return;
    }
    if (this.mode !== 'idle' && this.mode !== 'drag') {
      this.cancelOne();
      this.mode = 'spent';
    }
  };

  Recognizer.prototype.cancelOne = function () {
    if (this.one && this.one.long) this.timers.clear(this.one.long);
  };

  Recognizer.prototype.move = function (touches) {
    if (this.mode === 'one') {
      var p = touches[0];
      if (dist(p, this.one.start) <= TOUCH.tapSlop) return;
      this.cancelOne();
      if (this.one.second) {
        this.mode = 'drag';
        this.out.press(this.one.second);
        this.out.move(p);
      } else {
        this.mode = 'pan';
        this.out.pan(p.x - this.one.last.x, p.y - this.one.last.y);
      }
      this.one.last = p;
      return;
    }
    if (this.mode === 'pan') {
      this.out.pan(touches[0].x - this.one.last.x, touches[0].y - this.one.last.y);
      this.one.last = touches[0];
      return;
    }
    if (this.mode === 'drag') {
      this.out.move(touches[0]);
      this.one.last = touches[0];
      return;
    }
    if (this.mode === 'two' && touches.length >= 2) {
      var t = this.two;
      var d = dist(touches[0], touches[1]);
      var c = centre(touches[0], touches[1]);
      if (!t.kind) {
        if (Math.abs(d - t.d0) > TOUCH.twoFingerSlop) {
          t.kind = 'pinch';
          this.out.pinchStart();
        } else if (dist(c, t.c0) > TOUCH.twoFingerSlop) {
          t.kind = 'wheel';
          t.lastY = t.c0.y;
        } else {
          return;
        }
      }
      if (t.kind === 'pinch') {
        this.out.pinch(t.d0, t.c0, d, c);
      } else {
        var w = wheelSteps(t.carry, c.y - t.lastY, TOUCH.wheelStep);
        t.carry = w.carry;
        t.lastY = c.y;
        if (w.up || w.down) this.out.wheel(w.up, w.down);
      }
    }
  };

  Recognizer.prototype.up = function (remaining) {
    var self = this;
    if (this.mode === 'one') {
      this.cancelOne();
      if (this.one.second) {
        this.out.doubleClick(this.one.second);
      } else {
        var p = this.one.start;
        this.pendingTap = {
          p: p,
          timer: this.timers.set(function () {
            self.pendingTap = null;
            self.out.click(0, p);
          }, TOUCH.doubleTapMs)
        };
      }
      this.mode = 'idle';
      return;
    }
    if (this.mode === 'drag') {
      this.out.release(this.one.last);
      this.mode = remaining > 0 ? 'spent' : 'idle';
      return;
    }
    if (remaining === 0) this.mode = 'idle';
    else if (this.mode !== 'idle') this.mode = 'spent';
  };

  var api = {
    MAX_ZOOM: MAX_ZOOM,
    TOUCH: TOUCH,
    baseScale: baseScale,
    minZoom: minZoom,
    clampView: clampView,
    fitView: fitView,
    toRemote: toRemote,
    toScreen: toScreen,
    pinchView: pinchView,
    panView: panView,
    revealView: revealView,
    wheelSteps: wheelSteps,
    Recognizer: Recognizer
  };

  if (typeof module !== 'undefined' && module.exports) {
    module.exports = api;
  }
  if (typeof document === 'undefined' || typeof root.Guacamole === 'undefined') {
    return;
  }

  // ─── The page ─────────────────────────────────────────────────────────────

  var Guacamole = root.Guacamole;
  var viewport = document.getElementById('viewport');
  var stage = document.getElementById('stage');
  var marker = document.getElementById('pointer');

  var client = null;
  var generation = 0;
  var remote = { w: 0, h: 0 };
  var view = { zoom: 1, x: 0, y: 0 };
  var pinchFrom = null;
  var pointer = { x: 0, y: 0, shown: false, held: false };
  var keyboardOpen = false;
  var lastWidth = 0;

  // guacamole-common-js turns a close code into its own status and drops the
  // number, and the number is what says whether a fresh ticket will help. So
  // every socket the page opens reports its close code here first.
  var lastClose = { code: 0, reason: '' };
  var NativeWebSocket = root.WebSocket;
  function TrackedWebSocket(url, protocols) {
    var ws = new NativeWebSocket(url, protocols);
    ws.addEventListener('close', function (e) {
      lastClose = { code: e.code, reason: e.reason || '' };
    });
    return ws;
  }
  TrackedWebSocket.prototype = NativeWebSocket.prototype;
  ['CONNECTING', 'OPEN', 'CLOSING', 'CLOSED'].forEach(function (k) {
    TrackedWebSocket[k] = NativeWebSocket[k];
  });
  root.WebSocket = TrackedWebSocket;

  function post(event) {
    event.type = 'state';
    if (root.HeliosDesktop && root.HeliosDesktop.postMessage) {
      root.HeliosDesktop.postMessage(JSON.stringify(event));
    }
  }

  function size() {
    return { w: viewport.clientWidth, h: viewport.clientHeight };
  }

  function render() {
    if (!client || remote.w === 0) return;
    var v = size();
    var s = baseScale(v.w, remote.w) * view.zoom;
    client.getDisplay().scale(s);
    stage.style.transform = 'translate(' + view.x + 'px,' + view.y + 'px)';
    if (pointer.shown) {
      var p = toScreen(view, v.w, remote.w, pointer.x, pointer.y);
      marker.style.transform = 'translate(' + p.x + 'px,' + p.y + 'px)';
      marker.hidden = false;
      marker.classList.toggle('held', pointer.held);
    }
  }

  function relayout(reset) {
    var v = size();
    view = reset ? fitView(v.w, v.h, remote.w, remote.h) : clampView(view, v.w, v.h, remote.w, remote.h);
    render();
  }

  // ── Mouse ──

  function sendMouse(p, buttons) {
    if (!client) return;
    var v = size();
    var r = toRemote(view, v.w, remote.w, remote.h, p.x, p.y);
    pointer.x = r.x;
    pointer.y = r.y;
    pointer.shown = true;
    pointer.held = !!buttons.left;
    client.sendMouseState(
      new Guacamole.Mouse.State({
        x: r.x,
        y: r.y,
        left: !!buttons.left,
        middle: false,
        right: !!buttons.right,
        up: !!buttons.up,
        down: !!buttons.down
      }),
      false
    );
    render();
  }

  function click(button, p) {
    var held = button === 2 ? { right: true } : { left: true };
    sendMouse(p, {});
    sendMouse(p, held);
    sendMouse(p, {});
  }

  function wheelAt(button, n) {
    var p = toScreen(view, size().w, remote.w, pointer.x, pointer.y);
    for (var i = 0; i < n; i++) {
      var b = {};
      b[button] = true;
      sendMouse(p, b);
      sendMouse(p, {});
    }
  }

  var recognizer = new Recognizer(
    {
      click: click,
      right: function (p) { click(2, p); },
      doubleClick: function (p) { click(0, p); click(0, p); },
      press: function (p) { sendMouse(p, {}); sendMouse(p, { left: true }); },
      move: function (p) { sendMouse(p, { left: true }); },
      release: function (p) { sendMouse(p, {}); },
      wheel: function (up, down) { wheelAt('up', up); wheelAt('down', down); },
      pan: function (dx, dy) {
        var v = size();
        view = panView(view, v.w, v.h, remote.w, remote.h, dx, dy);
        render();
      },
      pinchStart: function () { pinchFrom = view; },
      pinch: function (d0, c0, d, c) {
        var v = size();
        view = pinchView(pinchFrom || view, v.w, v.h, remote.w, remote.h, d0, c0.x, c0.y, d, c.x, c.y);
        render();
      }
    },
    {
      set: function (fn, ms) { return root.setTimeout(fn, ms); },
      clear: function (id) { root.clearTimeout(id); }
    }
  );

  function points(list) {
    var rect = viewport.getBoundingClientRect();
    var out = [];
    for (var i = 0; i < list.length; i++) {
      out.push({ x: list[i].clientX - rect.left, y: list[i].clientY - rect.top });
    }
    return out;
  }

  viewport.addEventListener('touchstart', function (e) {
    e.preventDefault();
    if (remote.w > 0) recognizer.down(points(e.touches));
  }, { passive: false });
  viewport.addEventListener('touchmove', function (e) {
    e.preventDefault();
    if (remote.w > 0) recognizer.move(points(e.touches));
  }, { passive: false });
  function touchEnd(e) {
    e.preventDefault();
    if (remote.w > 0) recognizer.up(e.touches.length);
  }
  viewport.addEventListener('touchend', touchEnd, { passive: false });
  viewport.addEventListener('touchcancel', touchEnd, { passive: false });

  root.addEventListener('resize', function () {
    var v = size();
    // A turn of the phone changes the width and starts the view again; the
    // keyboard changes only the height, and the view keeps its place.
    var turned = v.w !== lastWidth;
    lastWidth = v.w;
    relayout(turned);
    if (!turned && keyboardOpen && pointer.shown) {
      var p = toScreen(view, v.w, remote.w, pointer.x, pointer.y);
      view = revealView(view, v.w, v.h, remote.w, remote.h, p.x, p.y, 24);
      render();
    }
  });

  // ── The connection ──

  function disconnect() {
    generation++;
    if (client) {
      var old = client;
      client = null;
      old.disconnect();
    }
    stage.textContent = '';
    marker.hidden = true;
    pointer.shown = false;
    remote = { w: 0, h: 0 };
  }

  function connect(params) {
    disconnect();
    var mine = ++generation;
    lastClose = { code: 0, reason: '' };
    var tunnel = new Guacamole.WebSocketTunnel('../api/desktop/tunnel');
    var c = new Guacamole.Client(tunnel);
    client = c;
    var display = c.getDisplay();
    stage.appendChild(display.getElement());
    lastWidth = size().w;

    var errorText = '';
    display.onresize = function (w, h) {
      if (mine !== generation) return;
      remote = { w: w, h: h };
      relayout(true);
    };
    c.onerror = function (status) {
      errorText = (status && status.message) || errorText;
    };
    tunnel.onerror = function (status) {
      errorText = (status && status.message) || errorText;
    };
    // The client does not watch its own tunnel, so a socket the daemon
    // refuses or drops is heard about here; a disconnect the server asks for
    // arrives as the client's state. Either way the app hears it once.
    function closed() {
      if (mine !== generation) return;
      generation++;
      client = null;
      // Stops its keep-alive; the bumped generation keeps this from echoing.
      c.disconnect();
      post({
        state: 'closed',
        code: lastClose.code,
        message: lastClose.reason || errorText
      });
    }
    tunnel.onstatechange = function (state) {
      if (state === Guacamole.Tunnel.State.CLOSED) closed();
    };
    c.onstatechange = function (state) {
      if (mine !== generation) return;
      if (state === Guacamole.Client.State.CONNECTED) {
        post({ state: 'connected', code: 0, message: '' });
      } else if (state === Guacamole.Client.State.DISCONNECTED) {
        closed();
      }
    };

    post({ state: 'connecting', code: 0, message: '' });
    var query = ['ticket', 'width', 'height', 'dpi']
      .filter(function (k) { return params[k] !== undefined && params[k] !== ''; })
      .map(function (k) { return k + '=' + encodeURIComponent(params[k]); })
      .join('&');
    c.connect(query);
  }

  root.heliosDesktop = {
    connect: connect,
    resize: function (w, h) {
      if (client) client.sendSize(w, h);
    },
    setKeyboard: function (open) {
      keyboardOpen = !!open;
    },
    /** Presses the keys in order and lets go in reverse: one chord. */
    sendKeys: function (keysyms) {
      if (!client || !keysyms || !keysyms.length) return;
      var i;
      for (i = 0; i < keysyms.length; i++) client.sendKeyEvent(1, keysyms[i]);
      for (i = keysyms.length - 1; i >= 0; i--) client.sendKeyEvent(0, keysyms[i]);
    },
    disconnect: disconnect
  };

  // The first ticket comes in the fragment, which a browser never sends to a
  // server. It is wiped from the address at once: it works only once anyway,
  // but nothing should keep it.
  var hash = root.location.hash.replace(/^#/, '');
  if (hash) {
    var params = {};
    hash.split('&').forEach(function (pair) {
      var cut = pair.indexOf('=');
      if (cut > 0) params[decodeURIComponent(pair.slice(0, cut))] = decodeURIComponent(pair.slice(cut + 1));
    });
    root.history.replaceState(null, '', root.location.pathname);
    if (params.ticket) connect(params);
  }
})(typeof window !== 'undefined' ? window : globalThis);
