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
              'aws_route_table->aws_internet_gateway', 'aws_route_table->aws_nat_gateway'];
  for (var w = 0; w < want.length; w++) {
    if (!kinds[want[w]]) throw new Error('missing link direction ' + want[w]);
  }
  // The reverse of containment must not appear: the VPC leads to its subnets.
  if (kinds['aws_subnet->aws_vpc']) throw new Error('containment points the wrong way');
  // Each hop is one column wide, so nothing may skip the route tables.
  var skips = ['aws_vpc->aws_route_table', 'aws_vpc->aws_nat_gateway',
               'aws_vpc->aws_internet_gateway', 'aws_subnet->aws_nat_gateway',
               'aws_subnet->aws_internet_gateway'];
  for (var s = 0; s < skips.length; s++) {
    if (kinds[skips[s]]) throw new Error('a link skips a column: ' + skips[s]);
  }
});

check('hovering a subnet traces its own path and not a sibling', function () {
  var vpcWs = null;
  for (var i = 0; i < STATE.workspaces.length; i++) {
    if (STATE.workspaces[i].name === 'vpc') vpcWs = STATE.workspaces[i];
  }
  var m = buildMap(vpcWs);
  var traced = tracePath('aws_subnet.public[0]', adjacencyOf(m.links));
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
  var traced = tracePath('aws_route_table.public', adjacencyOf(m.links));
  if (!traced.nodes.has('aws_subnet.public[0]')) throw new Error('lost an associated subnet');
  if (!traced.nodes.has('aws_subnet.public[1]')) throw new Error('lost an associated subnet');
  if (!traced.nodes.has('aws_internet_gateway.main')) throw new Error('lost the gateway');
});

check('ribbons stay hidden until something is hovered', function () {
  renderMap(STATE);
  var drawn = document.getElementById('ribbons').children;
  if (!drawn.length) throw new Error('no ribbons drawn');
  var directed = 0;
  for (var i = 0; i < drawn.length; i++) {
    if (drawn[i].getAttribute('stroke-opacity') !== '0') {
      throw new Error('a ribbon is visible before anything is hovered');
    }
    if (drawn[i].getAttribute('marker-end')) throw new Error('an arrowhead survived');
    if (drawn[i].dataset.from && drawn[i].dataset.to) directed++;
  }
  if (directed !== drawn.length) throw new Error('ribbons missing a direction');
  print('       (' + drawn.length + ' directed ribbons)');
});

check('hovering reveals the traced path only', function () {
  renderMap(STATE);
  hoverApi.hover('aws_subnet.public[0]');
  var drawn = document.getElementById('ribbons').children;
  var hot = [], cold = 0;
  for (var i = 0; i < drawn.length; i++) {
    if (drawn[i].getAttribute('stroke-opacity') !== '0') {
      hot.push(drawn[i].dataset.from + ' -> ' + drawn[i].dataset.to);
    } else cold++;
  }
  if (!hot.length) throw new Error('hover revealed nothing');
  if (!cold) throw new Error('hover revealed every ribbon rather than a path');
  var joined = hot.join(' | ');
  if (joined.indexOf('aws_vpc.main -> aws_subnet.public[0]') === -1) {
    throw new Error('no arrow from the VPC: ' + joined);
  }
  if (joined.indexOf('aws_subnet.public[0] -> aws_route_table.public') === -1) {
    throw new Error('no arrow to the route table: ' + joined);
  }
  if (joined.indexOf('aws_route_table.public -> aws_internet_gateway.main') === -1) {
    throw new Error('no arrow on to the gateway: ' + joined);
  }
  hoverApi.clear();
  for (var j = 0; j < drawn.length; j++) {
    if (drawn[j].getAttribute('stroke-opacity') !== '0') {
      throw new Error('ribbons stayed visible after the hover ended');
    }
  }
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

check('a VPC joins its subnets and nothing further', function () {
  filter.text = ''; filter.statuses = new Set();
  for (var i = 0; i < STATE.workspaces.length; i++) {
    var ws = STATE.workspaces[i];
    var m = buildMap(ws);
    var out = {};
    for (var k = 0; k < m.links.length; k++) {
      (out[m.links[k].from] = out[m.links[k].from] || {})[m.links[k].to] = true;
    }
    for (var p = 0; p < m.panels.length; p++) {
      var panel = m.panels[p];
      for (var n = 0; n < panel.subnets.length; n++) {
        if (!(out[panel.vpc.id] || {})[panel.subnets[n].id]) {
          throw new Error(ws.name + ': subnet ' + panel.subnets[n].id + ' unjoined');
        }
      }
      // Route tables and gateways are reached through the subnets, so a VPC
      // must not short-circuit straight to them.
      var beyond = panel.routeTables.concat(panel.gateways);
      for (var b = 0; b < beyond.length; b++) {
        if ((out[panel.vpc.id] || {})[beyond[b].id]) {
          throw new Error(ws.name + ': vpc points straight at ' + beyond[b].id);
        }
      }
    }
  }
});

check('resources beside the columns are grouped by type', function () {
  filter.text = ''; filter.statuses = new Set();
  renderMap(STATE);
  var h = __sinks['mapbody'] || '';
  if (h.indexOf('class="cat"') === -1) throw new Error('no category groups rendered');
  var headings = {}, re = /<span>([^<]*)<\/span>\s*<em>(\d+)<\/em><\/h4>/g, m;
  while ((m = re.exec(h)) !== null) headings[m[1]] = Number(m[2]);
  // Instances sit inside their subnet, so what stands beside the columns here
  // is the storage, the load balancing and the security groups.
  ['EBS volume', 'Load balancer', 'Security group'].forEach(function (want) {
    if (!headings[want]) throw new Error('no category for ' + want +
      '; got ' + Object.keys(headings).join(', '));
  });
  if (headings['EBS volume'] !== 2) throw new Error('wrong count for EBS volume');
  if (!/<h4>\s*<svg/.test(h)) throw new Error('category heading has no icon');
});

check('hovering an instance reaches its subnet, interfaces and volumes', function () {
  var ec2 = null;
  for (var i = 0; i < STATE.workspaces.length; i++) {
    if (STATE.workspaces[i].name === 'ec2') ec2 = STATE.workspaces[i];
  }
  if (!ec2) throw new Error('no ec2 workspace in fixtures');
  var m = buildMap(ec2), byId = {};
  for (var j = 0; j < ec2.nodes.length; j++) byId[ec2.nodes[j].id] = ec2.nodes[j];
  var traced = tracePath('aws_instance.app[0]', adjacencyOf(m.links));
  var kinds = {};
  traced.nodes.forEach(function (id) { if (byId[id]) kinds[byId[id].type] = true; });
  ['aws_subnet', 'aws_ebs_volume', 'aws_security_group'].forEach(function (t) {
    if (!kinds[t]) {
      throw new Error('instance did not reach ' + t + '; reached ' + Object.keys(kinds).join(', '));
    }
  });
  // The volume is bound by an attachment, which must be collapsed rather
  // than left as a dead end.
  if (!traced.nodes.has('aws_ebs_volume.data[0]')) {
    throw new Error('did not reach the volume attached to this instance');
  }
  if (traced.nodes.has('aws_ebs_volume.data[1]')) {
    throw new Error('reached another instance volume');
  }
});

check('a relationship is one hop, so hovering does not sprawl', function () {
  var ec2 = null;
  for (var i = 0; i < STATE.workspaces.length; i++) {
    if (STATE.workspaces[i].name === 'ec2') ec2 = STATE.workspaces[i];
  }
  var m = buildMap(ec2);
  var traced = tracePath('aws_instance.app[0]', adjacencyOf(m.links));
  // The load balancer reaches every subnet; an instance sharing a target
  // group with it must not inherit that whole spread.
  if (traced.nodes.has('aws_subnet.web[1]')) {
    throw new Error('hovering an instance dragged in an unrelated subnet');
  }
  if (traced.nodes.size > 12) {
    throw new Error('an instance lit up ' + traced.nodes.size + ' resources');
  }
  // A subnet still reads end to end along the VPC path.
  var viaSubnet = tracePath('aws_subnet.web[0]', adjacencyOf(m.links));
  if (!viaSubnet.nodes.has('aws_internet_gateway.app')) {
    throw new Error('the VPC path stopped short of the gateway');
  }
});

check('pinning holds the path, and survives a redraw', function () {
  filter.text = ''; filter.statuses = new Set();
  renderMap(STATE);
  var drawn = document.getElementById('ribbons').children;
  function visible() {
    var n = 0;
    for (var i = 0; i < drawn.length; i++) {
      if (drawn[i].getAttribute('stroke-opacity') !== '0') n++;
    }
    return n;
  }
  hoverApi.pin('aws_subnet.public[0]');
  var lit = visible();
  if (!lit) throw new Error('pinning revealed nothing');
  if (hoverApi.pinned() !== 'aws_subnet.public[0]') throw new Error('pin not recorded');

  // Moving the pointer away must not drop a pinned path.
  hoverApi.clear();
  if (visible() !== lit) throw new Error('the pinned path was cleared by a mouse-out');

  // A re-plan redraws everything; the pin should come back with it.
  renderMap(STATE);
  drawn = document.getElementById('ribbons').children;
  if (hoverApi.pinned() !== 'aws_subnet.public[0]') throw new Error('pin lost on redraw');
  if (!visible()) throw new Error('the pinned path did not survive the redraw');

  hoverApi.pin(null);
  if (hoverApi.pinned()) throw new Error('pin not released');
  if (visible()) throw new Error('releasing the pin left ribbons behind');
});

check('a pin on a card that disappears is dropped', function () {
  renderMap(STATE);
  hoverApi.pin('aws_subnet.public[0]');
  filter.statuses = new Set(['destroy']); // fixtures have none, so all cards go
  renderMap(STATE);
  if (hoverApi.pinned()) throw new Error('pinned a card that is no longer drawn');
  filter.statuses = new Set();
  renderMap(STATE);
});

check('a mixed column is grouped, a single-type one is not', function () {
  filter.text = ''; filter.statuses = new Set();
  renderMap(STATE);
  var h = __sinks['mapbody'] || '';
  var headings = {}, re = /<span>([^<]*)<\/span>\s*<em>(\d+)<\/em><\/h4>/g, m;
  while ((m = re.exec(h)) !== null) headings[m[1]] = Number(m[2]);
  // The vpc fixture's network connections hold gateways of two kinds.
  ['NAT gateway', 'Internet gateway'].forEach(function (want) {
    if (!headings[want]) {
      throw new Error('no category heading for ' + want +
        '; got ' + Object.keys(headings).join(', '));
    }
  });
  // Route tables are all one type, so they are listed without a heading.
  if (headings['Route table']) {
    throw new Error('a single-type column was given a redundant heading');
  }
});

check('categories flow across the width', function () {
  var h = __sinks['mapbody'] || '';
  if (h.indexOf('class="cats"') === -1) {
    throw new Error('categories are not laid out to share the row');
  }
});

check('a resource is only in a VPC when it reaches one', function () {
  filter.text = ''; filter.statuses = new Set();
  var simple = null;
  for (var i = 0; i < STATE.workspaces.length; i++) {
    if (STATE.workspaces[i].name === 'ec2') simple = STATE.workspaces[i];
  }
  var m = buildMap(simple);
  // Everything placed in a panel must actually reach the VPC.
  var deps = {};
  for (var e = 0; e < simple.edges.length; e++) {
    (deps[simple.edges[e].from] = deps[simple.edges[e].from] || []).push(simple.edges[e].to);
  }
  var byId = {};
  for (var j = 0; j < simple.nodes.length; j++) byId[simple.nodes[j].id] = simple.nodes[j];
  m.panels.forEach(function (p) {
    p.inVpc.concat(p.subnets, p.routeTables, p.gateways).forEach(function (n) {
      if (n.meta && n.meta.scope === 'vpc') return; // in one by definition
      var reaches = (deps[n.id] || []).some(function (d) {
        return byId[d] && (byId[d].type === 'aws_vpc' ||
          (deps[d] || []).some(function (dd) { return byId[dd] && byId[dd].type === 'aws_vpc'; }));
      });
      if (!reaches) throw new Error(n.id + ' was placed in a VPC it does not reach');
    });
  });
});

check('resources outside a VPC are filed by their real scope', function () {
  renderMap(STATE);
  var h = __sinks['mapbody'] || '';
  // The simple workspace is random_pet and local_file: neither is AWS.
  if (h.indexOf('Outside AWS') === -1) {
    throw new Error('non-AWS resources were not filed outside AWS');
  }
  // Scope comes from the plan, not from a guess about the type.
  var kinds = {};
  STATE.workspaces.forEach(function (ws) {
    ws.nodes.forEach(function (n) { kinds[n.type] = n.meta && n.meta.scope; });
  });
  if (kinds['random_pet'] !== 'external') throw new Error('random_pet is not external');
  // A subnet cannot exist outside a VPC; an SSM parameter or an EIP can.
  if (kinds['aws_subnet'] !== 'vpc') throw new Error('aws_subnet is not vpc-scoped');
  if (kinds['aws_route_table'] !== 'vpc') throw new Error('aws_route_table is not vpc-scoped');
  // An Elastic IP is not inside a VPC, but it serves one, so it is drawn
  // beside the VPC its NAT gateway attaches it to.
  if (kinds['aws_eip'] !== 'network') throw new Error('aws_eip is not network-scoped');
  if (kinds['aws_ssm_parameter'] && kinds['aws_ssm_parameter'] !== 'region') {
    throw new Error('aws_ssm_parameter should be region-scoped');
  }
});

check('networking that serves a VPC is drawn with it', function () {
  filter.text = ''; filter.statuses = new Set();
  var vpcWs = null;
  for (var i = 0; i < STATE.workspaces.length; i++) {
    if (STATE.workspaces[i].name === 'vpc') vpcWs = STATE.workspaces[i];
  }
  var m = buildMap(vpcWs);
  var inPanel = {};
  m.panels.forEach(function (p) {
    p.inVpc.concat(p.subnets, p.routeTables, p.gateways).forEach(function (n) {
      inPanel[n.id] = true;
    });
  });
  // The EIPs are held by NAT gateways in this VPC, so they belong with it.
  ['aws_eip.nat[0]', 'aws_eip.nat[1]'].forEach(function (id) {
    if (!inPanel[id]) throw new Error(id + ' was left out of the VPC it serves');
  });
  // And it did not drag in things that merely happen to be nearby.
  m.scopes.region.concat(m.scopes.account, m.scopes.external).forEach(function (n) {
    if (n.type === 'aws_eip') throw new Error('an attached EIP stayed outside');
  });
});

check('links are not duplicated', function () {
  for (var i = 0; i < STATE.workspaces.length; i++) {
    var m = buildMap(STATE.workspaces[i]), seen = {};
    for (var k = 0; k < m.links.length; k++) {
      var key = m.links[k].from + ' ' + m.links[k].to;
      if (seen[key]) throw new Error('duplicate link ' + key);
      seen[key] = true;
    }
  }
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
