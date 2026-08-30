// Minimal DOM and browser-API stubs so index.html's script can be executed
// headlessly by jsc. Only what the page actually touches is implemented;
// innerHTML assignments are captured in __sinks so the tests can inspect the
// markup the page produced.
var __sinks = {};

function __fakeEl(id) {
  return {
    id: id, _html: '', _text: '', dataset: {}, style: {},
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    setAttribute() {}, getAttribute() { return null; },
    appendChild() {}, insertAdjacentHTML() {},
    addEventListener() {}, removeEventListener() {},
    querySelector() { return null; }, querySelectorAll() { return []; },
    getBoundingClientRect() {
      return { left: 0, top: 0, width: 1200, height: 800, right: 1200, bottom: 800 };
    },
    scrollWidth: 1200, scrollHeight: 800, scrollLeft: 0, scrollTop: 0,
    get innerHTML() { return this._html; },
    set innerHTML(v) { this._html = v; __sinks[this.id] = v; },
    get textContent() { return this._text; },
    set textContent(v) { this._text = v; },
    set onclick(v) {}, focus() {},
  };
}

var document = {
  _els: {},
  getElementById(id) {
    return this._els[id] || (this._els[id] = __fakeEl(id));
  },
  createElementNS(ns, name) { return __fakeEl(name); },
  createElement(name) { return __fakeEl(name); },
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
