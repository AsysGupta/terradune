// Assertions run against the real page script after it has been loaded with
// the stub DOM. STATE is injected by the Go test from real plan fixtures.
var __failures = 0;

function check(name, fn) {
  try {
    fn();
    print('  ok   ' + name);
  } catch (e) {
    __failures++;
    print('  FAIL ' + name + ' -> ' + e);
  }
}

check('renderMap runs over every workspace', function () {
  renderMap(STATE);
});

check('map produced markup', function () {
  var h = __sinks['mapbody'] || '';
  if (h.length < 500) throw new Error('suspiciously small: ' + h.length);
});

check('every card carries its workspace and address', function () {
  var h = __sinks['mapbody'] || '';
  var cards = (h.match(/class="card /g) || []).length;
  var ws = (h.match(/data-ws="/g) || []).length;
  var ids = (h.match(/data-id="/g) || []).length;
  if (!cards) throw new Error('no cards rendered');
  if (cards !== ws || cards !== ids) {
    throw new Error(cards + ' cards but ' + ws + ' data-ws / ' + ids + ' data-id');
  }
});

check('every card carries an icon', function () {
  var h = __sinks['mapbody'] || '';
  var cards = (h.match(/class="card /g) || []).length;
  var icons = (h.match(/class="ico"/g) || []).length;
  if (icons !== cards) throw new Error(cards + ' cards but ' + icons + ' icons');
});

check('known AWS types get a service glyph rather than the fallback', function () {
  var types = ['aws_vpc', 'aws_subnet', 'aws_route_table', 'aws_instance',
               'aws_nat_gateway', 'aws_internet_gateway', 'aws_security_group',
               'aws_ebs_volume', 'aws_lb', 'aws_eip', 'aws_route'];
  for (var i = 0; i < types.length; i++) {
    var svg = iconSVG(types[i]);
    if (svg.indexOf('<svg') !== 0) throw new Error(types[i] + ' produced no svg');
    if (svg.indexOf('#7D8998') !== -1) throw new Error(types[i] + ' fell back to grey');
  }
});

check('unknown types still render an icon', function () {
  if (iconSVG('random_pet').indexOf('<svg') !== 0) throw new Error('no fallback icon');
});

check('resources living in one subnet are nested inside it', function () {
  var h = __sinks['mapbody'] || '';
  if (h.indexOf('class="contents"') === -1) throw new Error('nothing nested');
});

check('attribute rows handle unknown, changed and nested values', function () {
  var out = attrRows({ a: 'x', nested: { k: [1, 2] }, ch: 'new' }, { ch: 'old' }, ['later']);
  if (out.indexOf('known after apply') === -1) throw new Error('unknown not rendered');
  if (out.indexOf('class="was"') === -1) throw new Error('before/after diff missing');
  if (out.indexOf('<pre>') === -1) throw new Error('nested value not rendered');
});

check('route summary shows destination and pending target', function () {
  var s = summarize('aws_route', { destination_cidr_block: '0.0.0.0/0' }, ['gateway_id']);
  if (s.indexOf('0.0.0.0/0') === -1) throw new Error('destination missing: ' + s);
  if (s.indexOf('known after apply') === -1) throw new Error('unknown target missing: ' + s);
});

check('related rows render an attached route', function () {
  var h = relatedHTML({
    address: 'aws_route.public_internet', type: 'aws_route', status: 'create',
    after: { destination_cidr_block: '0.0.0.0/0' }, unknown: ['gateway_id'],
  });
  if (h.indexOf('0.0.0.0/0') === -1) throw new Error('destination missing');
  if (h.indexOf('<svg') === -1) throw new Error('icon missing');
});

check('graph hierarchy terminates and covers every workspace', function () {
  var root = toElk(STATE);
  if (!root.children || !root.children.length) throw new Error('no workspace groups');
  if (!root.edges.length) throw new Error('no edges');
  var names = {};
  for (var i = 0; i < root.children.length; i++) names[root.children[i].label] = true;
  for (var j = 0; j < STATE.workspaces.length; j++) {
    if (!names[STATE.workspaces[j].name]) {
      throw new Error('missing group for ' + STATE.workspaces[j].name);
    }
  }
});

check('markup escaping neutralises injected html', function () {
  if (esc('<img src=x onerror=1>').indexOf('<') !== -1) throw new Error('not escaped');
});

// --- review mode: search and status filtering ---------------------------
function cardCount() {
  return ((__sinks['mapbody'] || '').match(/class="card /g) || []).length;
}

var __all = cardCount();

check('search narrows the map', function () {
  filter.text = 'nat';
  renderMap(STATE);
  var n = cardCount();
  if (n === 0) throw new Error('search for "nat" matched nothing');
  if (n >= __all) throw new Error('search did not narrow: ' + n + ' of ' + __all);
  var h = __sinks['mapbody'] || '';
  if (h.indexOf('aws_security_group') !== -1) throw new Error('unrelated resource survived');
});

check('search matches attribute values, not just names', function () {
  filter.text = '10.0.100.0';
  renderMap(STATE);
  if (cardCount() === 0) throw new Error('cidr search matched nothing');
});

check('a matching resource keeps the subnet that contains it', function () {
  filter.text = 'app-server-0';
  renderMap(STATE);
  var h = __sinks['mapbody'] || '';
  if (h.indexOf('class="contents"') === -1) throw new Error('nested match lost its subnet');
});

check('status filter shows only that status', function () {
  filter.text = '';
  filter.statuses = new Set(['destroy']);
  renderMap(STATE);
  if (cardCount() !== 0) throw new Error('fixtures have no destroys, got ' + cardCount());
  filter.statuses = new Set(['create']);
  renderMap(STATE);
  if (cardCount() === 0) throw new Error('create filter matched nothing');
  var h = __sinks['mapbody'] || '';
  if (h.indexOf('class="card existing') !== -1) throw new Error('non-create card survived');
});

check('the graph honours the same filter', function () {
  filter.statuses = new Set(['destroy']);
  var root = toElk(STATE);
  if (root.edges.length !== 0) throw new Error('edges survived an empty filter');
});

check('module for_each keys name their resources', function () {
  var n = { id: 'module.rt["web-az1"].aws_route_table.rt[0]', type: 'aws_route_table',
            name: 'rt', module: 'module.rt["web-az1"]', status: 'create' };
  if (displayName(n) !== 'web-az1') {
    throw new Error('got ' + displayName(n) + ', want the module key');
  }
  if (cardHTML(n).indexOf('rt[0]') === -1) {
    throw new Error('resource name dropped from the card');
  }
  // A Name tag still wins, and an unkeyed resource keeps its own name.
  var tagged = { id: 'aws_vpc.main', type: 'aws_vpc', name: 'main', module: '',
                 status: 'create', meta: { name: 'prod-vpc' } };
  if (displayName(tagged) !== 'prod-vpc') throw new Error('Name tag ignored');
  var plain = { id: 'aws_eip.n[1]', type: 'aws_eip', name: 'n', module: '', status: 'create' };
  if (displayName(plain) !== 'n[1]') throw new Error('got ' + displayName(plain));
});

// --- directional path tracing on hover ----------------------------------
check('links point along the columns, vpc -> subnet -> route table -> gateway', function () {
  filter.text = ''; filter.statuses = new Set();
  var vpcWs = null;
  for (var i = 0; i < STATE.workspaces.length; i++) {
    if (STATE.workspaces[i].name === 'vpc') vpcWs = STATE.workspaces[i];
  }
  if (!vpcWs) throw new Error('no vpc workspace in fixtures');
  var m = buildMap(vpcWs);
  var byId = {};
  for (var j = 0; j < vpcWs.nodes.length; j++) byId[vpcWs.nodes[j].id] = vpcWs.nodes[j];
  var kinds = {};
  for (var k = 0; k < m.links.length; k++) {
    var l = m.links[k];
    kinds[byId[l.from].type + '->' + byId[l.to].type] = true;
  }
  var want = ['aws_vpc->aws_subnet', 'aws_subnet->aws_route_table',
              'aws_route_table->aws_internet_gateway', 'aws_subnet->aws_nat_gateway'];
  for (var w = 0; w < want.length; w++) {
    if (!kinds[want[w]]) throw new Error('missing link direction ' + want[w]);
  }
  // The reverse of containment must not appear: the VPC leads to its subnets.
  if (kinds['aws_subnet->aws_vpc']) throw new Error('containment points the wrong way');
});

check('hovering a subnet traces its own path and not a sibling', function () {
  var vpcWs = null;
  for (var i = 0; i < STATE.workspaces.length; i++) {
    if (STATE.workspaces[i].name === 'vpc') vpcWs = STATE.workspaces[i];
  }
  var m = buildMap(vpcWs);
  var out = new Map(), into = new Map();
  for (var k = 0; k < m.links.length; k++) {
    var l = m.links[k];
    if (!out.has(l.from)) out.set(l.from, new Set());
    if (!into.has(l.to)) into.set(l.to, new Set());
    out.get(l.from).add(l.to); into.get(l.to).add(l.from);
  }
  var traced = tracePath('aws_subnet.public[0]', out, into);
  if (!traced.nodes.has('aws_vpc.main')) throw new Error('lost the containing VPC');
  if (!traced.nodes.has('aws_route_table.public')) throw new Error('lost the route table');
  if (!traced.nodes.has('aws_internet_gateway.main')) throw new Error('lost the gateway');
  // public[1] shares that route table; reaching it would need a backwards hop.
  if (traced.nodes.has('aws_subnet.public[1]')) throw new Error('lit up a sibling subnet');
  if (traced.nodes.has('aws_subnet.private[0]')) throw new Error('lit up an unrelated subnet');
  // Edges are recorded in their drawn direction so the arrowheads match.
  if (!traced.edges.has('aws_subnet.public[0] aws_route_table.public')) {
    throw new Error('subnet -> route table edge not traced');
  }
  if (!traced.edges.has('aws_vpc.main aws_subnet.public[0]')) {
    throw new Error('vpc -> subnet edge not traced in its drawn direction');
  }
});

check('hovering a route table reaches its subnets and its gateway', function () {
  var vpcWs = null;
  for (var i = 0; i < STATE.workspaces.length; i++) {
    if (STATE.workspaces[i].name === 'vpc') vpcWs = STATE.workspaces[i];
  }
  var m = buildMap(vpcWs);
  var out = new Map(), into = new Map();
  for (var k = 0; k < m.links.length; k++) {
    var l = m.links[k];
    if (!out.has(l.from)) out.set(l.from, new Set());
    if (!into.has(l.to)) into.set(l.to, new Set());
    out.get(l.from).add(l.to); into.get(l.to).add(l.from);
  }
  var traced = tracePath('aws_route_table.public', out, into);
  if (!traced.nodes.has('aws_subnet.public[0]')) throw new Error('lost an associated subnet');
  if (!traced.nodes.has('aws_subnet.public[1]')) throw new Error('lost an associated subnet');
  if (!traced.nodes.has('aws_internet_gateway.main')) throw new Error('lost the gateway');
});

check('ribbons carry arrowheads and record their direction', function () {
  renderMap(STATE);
  var defs = __sinks['ribbons'] || '';
  if (defs.indexOf('id="arr-dim"') === -1 || defs.indexOf('id="arr-hot"') === -1) {
    throw new Error('arrow markers not defined');
  }
  var drawn = document.getElementById('ribbons').children;
  if (!drawn.length) throw new Error('no ribbons drawn');
  var withArrow = 0, directed = 0;
  for (var i = 0; i < drawn.length; i++) {
    if (drawn[i].getAttribute('marker-end')) withArrow++;
    if (drawn[i].dataset.from && drawn[i].dataset.to) directed++;
  }
  if (withArrow !== drawn.length) {
    throw new Error(withArrow + ' of ' + drawn.length + ' ribbons have arrowheads');
  }
  if (directed !== drawn.length) throw new Error('ribbons missing a direction');
  print('       (' + drawn.length + ' directed ribbons)');
});

check('a ribbon runs from the vpc to its subnet, not the reverse', function () {
  var drawn = document.getElementById('ribbons').children;
  var found = false, reversed = false;
  for (var i = 0; i < drawn.length; i++) {
    var f = drawn[i].dataset.from, t = drawn[i].dataset.to;
    if (f === 'aws_vpc.main' && t === 'aws_subnet.public[0]') found = true;
    if (t === 'aws_vpc.main' && f === 'aws_subnet.public[0]') reversed = true;
  }
  if (!found) throw new Error('no vpc -> subnet ribbon');
  if (reversed) throw new Error('ribbon drawn subnet -> vpc');
});

check('clearing filters restores every card', function () {
  filter.text = '';
  filter.statuses = new Set();
  renderMap(STATE);
  if (cardCount() !== __all) throw new Error('got ' + cardCount() + ', want ' + __all);
});

if (__failures) {
  print('\n' + __failures + ' FAILURE(S)');
} else {
  print('\nALL CHECKS PASSED');
}
