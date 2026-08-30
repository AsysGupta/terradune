// Minimal DOM and browser-API stubs so index.html's script can be executed
// headlessly by jsc. Only what the page actually touches is implemented;
// innerHTML assignments are captured in __sinks so the tests can inspect the
// markup the page produced.
var __sinks = {};

function __fakeEl(id, tag) {
  return {
    id: id, tag: tag || id, _html: '', _text: '', dataset: {}, style: {},
    attrs: {}, children: [],
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return this.attrs[k] === undefined ? null : this.attrs[k]; },
    appendChild(c) { this.children.push(c); return c; },
    insertAdjacentHTML() {},
    addEventListener() {}, removeEventListener() {},
    querySelector(sel) { return __cardFor(sel); },
    querySelectorAll(sel) { return sel === '.card' ? __allCards() : []; },
    getBoundingClientRect() {
      return { left: 0, top: 0, width: 1200, height: 800, right: 1200, bottom: 800 };
    },
    scrollWidth: 1200, scrollHeight: 800, scrollLeft: 0, scrollTop: 0,
    get innerHTML() { return this._html; },
    // Assigning innerHTML clears children, exactly as a browser would, so a
    // redraw does not accumulate the previous pass's nodes.
    set innerHTML(v) {
      this._html = v; this.children = []; __sinks[this.id] = v;
      if (this.id === 'mapbody') __indexCards(v);
    },
    get textContent() { return this._text; },
    set textContent(v) { this._text = v; },
    set onclick(v) {}, focus() {},
  };
}

// The rendered map is markup, so cards are recovered from it and given a
// synthetic layout. Placing them left to right in document order mirrors the
// real page, where the columns are emitted VPC, subnets, route tables,
// gateways — enough for the direction of a ribbon to mean something.
var __cards = [];

function __indexCards(html) {
  __cards = [];
  var re = /<div class="card ([a-z]+)"[^>]*data-id="([^"]*)" data-ws="([^"]*)"/g;
  var m;
  while ((m = re.exec(html)) !== null) {
    var el = __fakeEl('card:' + m[2], 'div');
    el.dataset.id = m[2].replace(/&quot;/g, '"');
    el.dataset.ws = m[3];
    var i = __cards.length;
    el.getBoundingClientRect = (function (idx) {
      return function () {
        return { left: idx * 200, top: (idx % 12) * 40, width: 160, height: 30,
                 right: idx * 200 + 160, bottom: (idx % 12) * 40 + 30 };
      };
    })(i);
    __cards.push(el);
  }
}

function __allCards() { return __cards; }

function __cardFor(sel) {
  var m = /data-id="((?:[^"\\]|\\.)*)"/.exec(sel || '');
  if (!m) return null;
  var want = m[1].replace(/\\"/g, '"').replace(/\\\\/g, '\\');
  for (var i = 0; i < __cards.length; i++) {
    if (__cards[i].dataset.id === want) return __cards[i];
  }
  return null;
}

var document = {
  _els: {},
  getElementById(id) {
    return this._els[id] || (this._els[id] = __fakeEl(id));
  },
  createElementNS(ns, name) { return __fakeEl(name, name); },
  createElement(name) { return __fakeEl(name, name); },
  addEventListener() {},
};
var window = { addEventListener() {} };
var CSS = { escape(s) { return String(s).replace(/["\\]/g, '\\$&'); } };
var EventSource = function () { this.onmessage = null; this.onerror = null; };
var ELK = function () {
  this.layout = function (g) {
    return Promise.resolve({ width: 100, height: 100, children: [], edges: [] });
  };
};
var fetch = function () {
  return Promise.resolve({ ok: true, json() { return Promise.resolve({}); } });
};
