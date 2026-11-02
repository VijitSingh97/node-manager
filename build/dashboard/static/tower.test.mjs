import { test } from "node:test";
import assert from "node:assert/strict";
import { project, gridCell, isRollover, nextDisplayed, retainedBlockFill, retainedConfirmedBlocks, smooth, nextHighlight, retainedSyncProgress, nextBuildProgress, constructionGeometry, overviewConstructionGeometry, overviewPartialBottomLimit, nextLayoutPoint, loadingHighlight, nextScene, scenePhase, overviewTransition, focusedLabelTransition, overviewHitWidth, overviewChoiceGeometry, overviewLabelPosition, towerLayout, overviewLayout, assignOverviewSlots, previewDepth, sceneDepth, overviewBlockLabels, NODE_IDS, GRID, PER_LAYER, SCENE_MS } from "./tower.js";

test("a layer is 12x12 = 144 blocks", () => {
  assert.equal(GRID, 12);
  assert.equal(PER_LAYER, 144);
});

test("gridCell fills row by row, back to front", () => {
  assert.deepEqual(gridCell(0), { gx: 0, gy: 0 });
  assert.deepEqual(gridCell(11), { gx: 11, gy: 0 });
  assert.deepEqual(gridCell(12), { gx: 0, gy: 1 });
  assert.deepEqual(gridCell(143), { gx: 11, gy: 11 });
});

test("isRollover fires only when the block layer wraps, not on a wobble", () => {
  assert.equal(isRollover(0, 140), true);
  assert.equal(isRollover(5, 140), true);
  assert.equal(isRollover(139, 140), false);
  assert.equal(isRollover(141, 140), false); // still climbing
});

test("nextDisplayed snaps down on a dip and never overshoots up", () => {
  assert.equal(nextDisplayed(100, 98, 1000), 98);
  assert.equal(nextDisplayed(100, 100, 1000), 100);
  assert.equal(nextDisplayed(100, 101, 100000), 101);
});

test("nextDisplayed catches up a large block gap", () => {
  const after = nextDisplayed(0, 100, 1000);
  assert.equal(after, 60);
});

test("nextDisplayed drifts a new block in over ~0.8s", () => {
  assert.ok(Math.abs(nextDisplayed(50, 51, 800) - 51) < 1e-9);
  assert.ok(nextDisplayed(50, 51, 400) < 51);
});

test("confirmed height chooses the next cube and wraps each 144-block layer", () => {
  assert.equal(retainedBlockFill("1"), 0);
  assert.equal(retainedBlockFill("144"), 143);
  assert.equal(retainedBlockFill("145"), 0);
  assert.equal(retainedBlockFill("290"), 1);
  assert.equal(retainedBlockFill("900719925474099312345"), Number((900719925474099312344n) % 144n));
});

test("block fill keeps the last confirmed height through missing updates", () => {
  assert.equal(retainedBlockFill(undefined, 37), 37);
  assert.equal(retainedBlockFill("", 37), 37);
  assert.equal(retainedBlockFill("unknown", 37), 37);
  assert.equal(retainedBlockFill("0", 37), 37);
});

test("confirmed block count distinguishes an empty chain from its first layer", () => {
  assert.equal(retainedConfirmedBlocks("1"), 0);
  assert.equal(retainedConfirmedBlocks("2"), 1);
  assert.equal(retainedConfirmedBlocks("145"), 144);
  assert.equal(retainedConfirmedBlocks("900719925474099312345"), Number.MAX_SAFE_INTEGER);
  assert.equal(retainedConfirmedBlocks(undefined, 27), 27);
});

test("project: height moves the point up; the diamond is symmetric", () => {
  assert.equal(project(2, 2, 1, 40, 20, 0, 0).y, project(2, 2, 0, 40, 20, 0, 0).y - 20);
  assert.equal(project(1, 0, 0, 40, 20, 0, 0).x, 20);
  assert.equal(project(0, 1, 0, 40, 20, 0, 0).x, -20);
});

test("smooth clamps to [0,1] and eases the ends", () => {
  assert.equal(smooth(-1), 0);
  assert.equal(smooth(2), 1);
  assert.equal(smooth(0.5), 0.5);
  assert.ok(smooth(0.25) < 0.25);
});

test("nextHighlight eases both into and out of the highlighted state", () => {
  const growing = nextHighlight(0, true, 60);
  assert.ok(growing > 0 && growing < 1);
  assert.ok(nextHighlight(growing, true, 60) > growing);
  const shrinking = nextHighlight(1, false, 60);
  assert.ok(shrinking > 0 && shrinking < 1);
  assert.ok(nextHighlight(shrinking, false, 60) < shrinking);
});

test("sync progress keeps the last usable value through transient outages", () => {
  assert.equal(retainedSyncProgress("42.5"), 42.5);
  assert.equal(retainedSyncProgress(undefined, 42.5), 42.5);
  assert.equal(retainedSyncProgress("", 42.5), 42.5);
  assert.equal(retainedSyncProgress("unavailable", 42.5), 42.5);
  assert.equal(retainedSyncProgress("120", 42.5), 100);
  assert.equal(retainedSyncProgress("-2", 42.5), 0);
});

test("tower build starts at the first known sync progress", () => {
  assert.equal(retainedSyncProgress(null), null);
  assert.equal(nextBuildProgress(null, 0.8, 100), 0.8);
});

test("tower build animates later progress changes and honors reduced motion", () => {
  const rising = nextBuildProgress(0.4, 0.8, 100);
  assert.ok(rising > 0.4 && rising < 0.8);
  assert.ok(nextBuildProgress(rising, 0.8, 100) > rising);
  const falling = nextBuildProgress(0.8, 0.4, 100);
  assert.ok(falling > 0.4 && falling < 0.8);
  assert.equal(nextBuildProgress(0.4, 0.8, 100, true), 0.8);
});

test("cube layers build upward while the tower base stays fixed", () => {
  const targetAnchor = 220;
  const depth = 12;
  const tw = 40;
  const bh = tw * 0.5;
  const stages = [0, 0.2, 0.5, 0.9, 1].map((progress) => constructionGeometry(targetAnchor, depth, tw, progress));
  const bases = stages.map((stage) => stage.anchorY + stage.depth * bh);
  assert.ok(bases.every((base) => base === bases[0]));
  assert.ok(stages.slice(1).every((stage, index) => stage.depth >= stages[index].depth));
  assert.equal(stages[0].depth, 0);
  assert.equal(stages[0].layerFill, 0);
  assert.equal(stages.at(-1).depth, depth);
  assert.equal(stages.at(-1).anchorY, targetAnchor);
  assert.equal(stages.at(-1).layerFill, null);

  const withinLayer = constructionGeometry(targetAnchor, depth, tw, 0.21);
  const laterInLayer = constructionGeometry(targetAnchor, depth, tw, 0.24);
  assert.equal(withinLayer.anchorY, laterInLayer.anchorY);
  assert.ok(laterInLayer.layerFill > withinLayer.layerFill);
});

test("the first confirmed blocks are literal cubes before a full layer exists", () => {
  const empty = constructionGeometry(220, 12, 40, 0, 0);
  const first = constructionGeometry(220, 12, 40, 1, 1);
  const partial = constructionGeometry(220, 12, 40, 0.000001, 17);
  const layer = constructionGeometry(220, 12, 40, 1, 144);
  assert.deepEqual({ depth: empty.depth, fill: empty.layerFill }, { depth: 0, fill: 0 });
  assert.deepEqual({ depth: first.depth, fill: first.layerFill }, { depth: 0, fill: 1 });
  assert.deepEqual({ depth: partial.depth, fill: partial.layerFill }, { depth: 0, fill: 17 });
  assert.deepEqual({ depth: layer.depth, fill: layer.layerFill }, { depth: 1, fill: 0 });
});

test("partial first layers stay above their cards and hover upward", () => {
  for (const [width, height] of [[1920, 720], [872, 888], [375, 667]]) {
    for (const count of [1, 3, 7]) {
      const layout = overviewLayout(width, count, height);
      const sortedAnchors = layout.anchors.slice().sort((a, b) => a - b);
      const minGap = count > 1
        ? Math.min(...sortedAnchors.slice(1).map((anchor, index) => anchor - sortedAnchors[index]))
        : Infinity;
      for (const targetAnchor of layout.anchors) {
        const baseTw = layout.tw;
        const hoverTw = baseTw * 1.12;
        const depth = previewDepth(height, targetAnchor, baseTw);
        for (const blocks of [1, 3, 143]) {
          const limit = overviewPartialBottomLimit(width, height, targetAnchor, baseTw, minGap);
          const occupiedBottom = (anchor, tw) => Math.max(...Array.from({ length: blocks }, (_, index) => {
            const { gx, gy } = gridCell(index);
            return project(gx + 1, gy + 1, 0, tw, tw * 0.5, 0, anchor - GRID * tw / 4).y;
          }));
          const resting = overviewConstructionGeometry(
            targetAnchor, depth, baseTw, baseTw, 1, blocks, limit);
          const hovered = overviewConstructionGeometry(
            targetAnchor, depth, baseTw, hoverTw, 1, blocks, limit);
          const restingBottom = occupiedBottom(resting.anchorY, baseTw);
          const hoveredBottom = occupiedBottom(hovered.anchorY, hoverTw);
          assert.ok(restingBottom <= limit + 1e-6, `${width}x${height}, count=${count}, blocks=${blocks}`);
          assert.ok(hoveredBottom <= restingBottom + 1e-6,
            `${width}x${height}, count=${count}, blocks=${blocks} hover moved down`);

          const actualTop = hovered.anchorY - hoverTw * 0.5;
          const diamondWidth = GRID * hoverTw * 1.12;
          const controlAnchor = width <= 700 ? Math.min(actualTop, targetAnchor) : actualTop;
          const geometry = overviewChoiceGeometry(width, controlAnchor, diamondWidth, minGap, height);
          const cardTop = geometry.top + geometry.labelTop;
          const cubeTop = hovered.anchorY - hoverTw * 0.5 - GRID * hoverTw / 4;
          assert.ok(hoveredBottom <= cardTop || cardTop + 44 <= cubeTop,
            `${width}x${height}, count=${count}, blocks=${blocks} cube overlaps its card`);
        }
      }
    }
  }
});

test("installed-node layouts reflow smoothly and handle an empty set", () => {
  assert.deepEqual(overviewLayout(900, 0, 700), {
    contentWidth: 900, tw: 0, positions: [], anchors: [],
  });
  assert.equal(overviewLayout(900, 3, 700).positions.length, 3);
  assert.equal(overviewLayout(900, 6, 700).positions.length, 6);

  const current = { x: 200, y: 300, tw: 30 };
  const target = { x: 500, y: 180, tw: 42 };
  const moving = nextLayoutPoint(current, target, 60);
  assert.ok(moving.x > current.x && moving.x < target.x);
  assert.ok(moving.y < current.y && moving.y > target.y);
  assert.ok(moving.tw > current.tw && moving.tw < target.tw);
  assert.deepEqual(nextLayoutPoint(current, target, 60, true), target);
});

test("a mobile foundation keeps its label and hit target inside the viewport", () => {
  const geometry = overviewChoiceGeometry(375, 690, 168, 56, 700);
  assert.ok(geometry.height >= 44);
  assert.ok(geometry.top >= 0);
  assert.ok(geometry.top + geometry.height <= 696);
  assert.ok(geometry.labelTop >= 2 && geometry.labelTop < geometry.height);
});

test("a desktop foundation keeps its label attached to the visible tower top", () => {
  const anchor = 680;
  const diamondWidth = 168;
  const geometry = overviewChoiceGeometry(1280, anchor, diamondWidth, Infinity, 700);
  assert.equal(geometry.top, anchor - diamondWidth / 4);
  assert.ok(geometry.labelTop >= 2);
  assert.ok(geometry.top + geometry.labelTop + 48 <= 700);
});

test("loading highlight fades faster than its tower and shrinks its glow", () => {
  assert.deepEqual(loadingHighlight(1, 0.5, 10), { opacity: 0.55, glow: 16 });
  assert.deepEqual(loadingHighlight(0.5, 0.5, 10), { opacity: 0.1375, glow: 8 });
  assert.deepEqual(loadingHighlight(0, 0.5, 10), { opacity: 0, glow: 0 });
});

test("nextScene reverses cleanly and changes towers through the overview", () => {
  const opening = nextScene("", "bitcoin", 0, 60);
  assert.equal(opening.node, "bitcoin");
  assert.ok(opening.progress > 0 && opening.progress < 1);

  const reversing = nextScene("bitcoin", "", opening.progress, 30);
  assert.equal(reversing.node, "bitcoin");
  assert.ok(reversing.progress < opening.progress);
  assert.ok(nextScene("bitcoin", "bitcoin", reversing.progress, 30).progress > reversing.progress);

  const switching = nextScene("bitcoin", "monero", 0, 16);
  assert.equal(switching.node, "monero");
  assert.ok(switching.progress > 0);
  const pendingSwitch = nextScene("bitcoin", "monero", 0.8, 30);
  assert.equal(pendingSwitch.node, "bitcoin");
  assert.ok(pendingSwitch.progress < 0.8);
  assert.deepEqual(nextScene("bitcoin", "monero", 1, 16, true), { node: "monero", progress: 1 });
});

test("scene timing is frame-rate independent and reverses without jumping", () => {
  const advance = (step) => {
    let scene = { node: "", progress: 0 };
    for (let elapsed = 0; elapsed < SCENE_MS; elapsed += step) {
      scene = nextScene(scene.node, "bitcoin", scene.progress, Math.min(step, SCENE_MS - elapsed));
    }
    return scene;
  };
  assert.deepEqual(advance(10), { node: "bitcoin", progress: 1 });
  assert.deepEqual(advance(100), { node: "bitcoin", progress: 1 });

  const halfway = nextScene("bitcoin", "bitcoin", 0, SCENE_MS / 2);
  const reversing = nextScene(halfway.node, "", halfway.progress, 140);
  const resumed = nextScene(reversing.node, "bitcoin", reversing.progress, 140);
  assert.equal(reversing.progress, 0.3);
  assert.equal(resumed.progress, halfway.progress);
});

test("neighbors disappear before zoom and return only after the camera is home", () => {
  for (const progress of [0, 0.1, 0.24, 0.25, 0.4, 0.7, 1]) {
    const phase = scenePhase(progress);
    if (phase.zoomAmount > 0) assert.equal(phase.overviewOpacity, 0, `progress=${progress}`);
    if (phase.overviewOpacity > 0) assert.equal(phase.zoomAmount, 0, `progress=${progress}`);
  }
  assert.deepEqual(scenePhase(0), { overviewOpacity: 1, zoomAmount: 0 });
  assert.deepEqual(scenePhase(1), { overviewOpacity: 0, zoomAmount: 1 });
});

test("overview controls fade with the other towers and only receive input when settled", () => {
  assert.deepEqual(overviewTransition(0, ""), { opacity: 1, hidden: false, interactive: true });
  assert.deepEqual(overviewTransition(0.125, "bitcoin"), { opacity: 0.5, hidden: false, interactive: false });
  assert.deepEqual(overviewTransition(0.5, "bitcoin"), { opacity: 0, hidden: true, interactive: false });
  assert.deepEqual(overviewTransition(1, "bitcoin"), { opacity: 0, hidden: true, interactive: false });
  assert.deepEqual(overviewTransition(0, "bitcoin"), { opacity: 1, hidden: false, interactive: false });
});

test("focused label follows opening, closing, switching, and reduced-motion scenes", () => {
  let scene = nextScene("", "bitcoin", 0, 200);
  const opening = focusedLabelTransition(scenePhase(scene.progress).zoomAmount, "974001");
  assert.equal(opening.hidden, false);
  assert.ok(opening.opacity > 0 && opening.opacity < 1);

  scene = nextScene(scene.node, "", scene.progress, 30);
  const closing = focusedLabelTransition(scenePhase(scene.progress).zoomAmount, "974001");
  assert.ok(closing.opacity < opening.opacity);

  scene = nextScene("bitcoin", "monero", 0, 200);
  const switched = focusedLabelTransition(scenePhase(scene.progress).zoomAmount, scene.node === "monero" ? "3512002" : "974001");
  assert.equal(scene.node, "monero");
  assert.ok(switched.opacity > 0 && switched.opacity < 1);

  scene = nextScene("bitcoin", "monero", 1, 16, true);
  assert.deepEqual(focusedLabelTransition(scene.progress, "3512002"), { opacity: 1, hidden: false });
  assert.deepEqual(focusedLabelTransition(0.5, ""), { opacity: 0, hidden: true });
});

test("renderer keeps the focused label live through opening, switching, and closing frames", async () => {
  const style = () => ({ setProperty(name, value) { this[name] = value; } });
  let fillCount = 0;
  let fillStyles = [];
  const context = new Proxy({ fill() { fillCount++; fillStyles.push(this.fillStyle); } }, { get(target, key) { return target[key] || (() => {}); } });
  const canvas = { getContext: () => context, style: style() };
  const label = { hidden: true, innerHTML: "", style: style() };
  const overview = { hidden: false, scrollLeft: 0, style: style(), addEventListener() {} };
  const choices = NODE_IDS.map((node) => ({
    dataset: { selectNode: node, nodeActive: "1" }, hidden: false, style: style(),
  }));
  const overviewLabels = Object.fromEntries(NODE_IDS.map((node) => [node, {
    hidden: true, style: style(), querySelector: () => ({ style: style(), textContent: "" }),
  }]));
  const live = Object.fromEntries(NODE_IDS.map((node) => [node, {
    dataset: {
      nextBlock: node === "bitcoin" ? "974001" : node === "monero" ? "3512002" : "1",
      syncPercent: node === "bitcoin" ? "33" : node === "monero" ? "62" : "100",
    },
  }]));
  const elements = { tower: canvas, "tower-label": label, "node-overview": overview };
  for (const node of NODE_IDS) {
    elements[`tower-overview-label-${node}`] = overviewLabels[node];
    elements[node === "bitcoin" ? "live" : `${node}-live`] = live[node];
  }
  const body = { dataset: { node: "", towerLabels: "1" } };
  const frames = [];
  globalThis.document = {
    body,
    hidden: false,
    documentElement: { getAttribute: () => "dark" },
    getElementById: (id) => elements[id] || null,
    querySelectorAll: () => choices,
    querySelector: (selector) => choices.find((choice) => selector.includes(`"${choice.dataset.selectNode}"`)) || null,
  };
  globalThis.window = {
    devicePixelRatio: 1,
    innerWidth: 1280,
    innerHeight: 900,
    matchMedia: () => ({ matches: false }),
    addEventListener() {},
  };
  globalThis.requestAnimationFrame = (callback) => { frames.push(callback); };
  let now = performance.now() + 100;
  const step = (dt) => {
    now += dt;
    const frame = frames.shift();
    assert.ok(frame, "renderer should schedule another frame");
    fillCount = 0;
    fillStyles = [];
    frame(now);
    return fillCount;
  };
  const bitcoinFillCount = () => fillStyles.filter((fill) =>
    ["#f2a900", "rgb(198, 139, 0)", "rgb(116, 81, 0)"].includes(fill)).length;
  const visibleTowerTop = () => {
    if (label.hidden) return null;
    const top = Number.parseFloat(label.style.top);
    assert.ok(Number.isFinite(top));
    assert.ok(top >= 8 && top < window.innerHeight, `partial-sync tower top left viewport: ${top}`);
    return top;
  };

  try {
    await import(`./tower.js?render-frames=${Date.now()}`);
    const initialBitcoinRows = bitcoinFillCount();
    body.dataset.hoverNode = "bitcoin";
    for (let i = 0; i < 35; i++) step(100);
    step(100);
    const overviewBitcoinRows = bitcoinFillCount();
    assert.equal(initialBitcoinRows, overviewBitcoinRows, "the first frame must start at the known sync height");
    assert.ok(Number(choices[0].style.zIndex) > Number(choices[2].style.zIndex),
      "a shorter syncing tower should stack in front of a synced neighbor");
    body.dataset.node = "bitcoin";
    step(60);
    assert.equal(label.hidden, true);
    assert.ok(Number(overview.style.opacity) > 0 && Number(overview.style.opacity) < 1);

    step(100);
    step(100);
    const openingOpacity = Number(label.style.opacity);
    const openingLeft = label.style.left;
    assert.match(label.innerHTML, /974001/);
    assert.ok(openingOpacity > 0 && openingOpacity < 1);
    assert.equal(label.hidden, false);
    let previousTop = visibleTowerTop();

    step(60);
    assert.notEqual(label.style.left, openingLeft);
    let currentTop = visibleTowerTop();
    assert.ok(Math.abs(currentTop - previousTop) < 200);
    previousTop = currentTop;
    assert.equal(overview.style.opacity, "0");
    const openingRows = [];
    for (let i = 0; i < 8 && Number(label.style.opacity) < 1; i++) {
      openingRows.push(step(100));
      assert.equal(bitcoinFillCount(), overviewBitcoinRows, "hover-then-focus must preserve completed cube rows");
    }
    assert.ok(openingRows.length > 1);
    assert.equal(new Set(openingRows).size, 1, "focus zoom must not remove completed cube rows");
    body.dataset.node = "monero";
    step(30);
    assert.match(label.innerHTML, /974001/);
    assert.ok(Number(label.style.opacity) < 1);
    currentTop = visibleTowerTop();
    assert.ok(Math.abs(currentTop - previousTop) < 200);
    for (let i = 0; i < 12 && !label.innerHTML.includes("3512002"); i++) {
      step(100);
      visibleTowerTop();
    }
    assert.match(label.innerHTML, /3512002/);
    assert.equal(label.hidden, false);
    for (let i = 0; i < 8 && Number(label.style.opacity) < 0.8; i++) {
      step(100);
      visibleTowerTop();
    }

    const beforeClose = Number(label.style.opacity);
    body.dataset.node = "";
    step(50);
    assert.ok(Number(label.style.opacity) < beforeClose);
    visibleTowerTop();
    assert.equal(overview.style.opacity, "0");
    for (let i = 0; i < 12 && !label.hidden; i++) {
      step(100);
      visibleTowerTop();
    }
    assert.equal(label.hidden, true);
  } finally {
    delete globalThis.document;
    delete globalThis.window;
    delete globalThis.requestAnimationFrame;
  }
});

test("renderer leaves block zero blank and draws one confirmed block as one cube", async () => {
  const render = async (nextBlock, syncPercent = "0") => {
    const fills = [];
    const style = () => ({ setProperty(name, value) { this[name] = value; } });
    const context = new Proxy({ fill() { fills.push(this.fillStyle); } }, {
      get(target, key) { return target[key] || (() => {}); },
    });
    const choice = { dataset: { selectNode: "tari", nodeActive: "1" }, hidden: false, style: style() };
    const elements = {
      tower: { getContext: () => context, style: style() },
      "node-overview": { hidden: false, scrollLeft: 0, style: style(), addEventListener() {} },
      "tari-live": { dataset: { nextBlock, syncPercent } },
    };
    globalThis.document = {
      body: { dataset: { node: "", towerLabels: "0" } },
      hidden: false,
      documentElement: { getAttribute: () => "light" },
      getElementById: (id) => elements[id] || null,
      querySelectorAll: () => [choice],
      querySelector: () => choice,
    };
    globalThis.window = {
      devicePixelRatio: 1,
      innerWidth: 1280,
      innerHeight: 900,
      matchMedia: () => ({ matches: true }),
      addEventListener() {},
    };
    globalThis.MutationObserver = class { observe() {} };
    try {
      await import(`./tower.js?early-blocks=${nextBlock}-${Date.now()}`);
      assert.equal(choice.dataset.nodeEmpty, nextBlock === "1" ? "1" : "0", "an empty node needs a visible label even with tower labels off");
      return fills.filter((fill) => fill === "#8f5cff" || fill === "rgb(112, 72, 199)" || fill === "rgb(74, 48, 133)").length;
    } finally {
      delete globalThis.document;
      delete globalThis.window;
      delete globalThis.MutationObserver;
    }
  };

  assert.equal(await render("1"), 0, "zero confirmed blocks should leave blank space");
  assert.equal(await render("2", "100"), 3, "one confirmed block should stay one cube at 100% of its target");
  assert.equal(await render("144", "100"), 143 * 3, "143 confirmed blocks should remain individual cubes");
  assert.equal(await render("145", "100"), 144 + 2, "144 confirmed blocks should become exactly one tower layer");
});

test("mature towers keep the next block visible in overview and focus", async () => {
  const style = () => ({ setProperty(name, value) { this[name] = value; } });
  let path = [];
  let pathFilled = false;
  let strokes = [];
  const saved = [];
  const context = new Proxy({
    shadowBlur: 0,
    globalAlpha: 1,
    beginPath() { path = []; pathFilled = false; },
    moveTo(x, y) { path.push([x, y]); },
    lineTo(x, y) { path.push([x, y]); },
    fill() { pathFilled = true; },
    stroke() {
      strokes.push({ path: path.map((point) => [...point]), filled: pathFilled, alpha: this.globalAlpha });
    },
    save() { saved.push({ shadowBlur: this.shadowBlur, globalAlpha: this.globalAlpha }); },
    restore() { Object.assign(this, saved.pop()); },
  }, { get(target, key) { return target[key] ?? (() => {}); } });
  const canvas = { getContext: () => context, style: style() };
  const label = { hidden: true, innerHTML: "", style: style() };
  const overview = { hidden: false, scrollLeft: 0, style: style(), addEventListener() {} };
  const choice = { dataset: { selectNode: "bitcoin", nodeActive: "1" }, hidden: false, style: style() };
  const live = { dataset: { nextBlock: "974305", syncPercent: "100" } };
  const body = { dataset: { node: "", towerLabels: "0" } };
  const elements = { tower: canvas, "tower-label": label, "node-overview": overview, live };
  const frames = [];
  let theme = "dark";
  globalThis.document = {
    body,
    hidden: false,
    documentElement: { getAttribute: () => theme },
    getElementById: (id) => elements[id] || null,
    querySelectorAll: () => [choice],
    querySelector: () => choice,
  };
  globalThis.window = {
    devicePixelRatio: 1,
    innerWidth: 1280,
    innerHeight: 900,
    matchMedia: () => ({ matches: false }),
    addEventListener() {},
  };
  globalThis.requestAnimationFrame = (callback) => { frames.push(callback); };
  let now = performance.now() + 100;
  const step = (count = 1) => {
    for (let i = 0; i < count; i++) {
      now += 100;
      strokes = [];
      const frame = frames.shift();
      assert.ok(frame, "renderer should schedule another frame");
      frame(now);
    }
  };
  const pending = () => strokes.filter((stroke) =>
    stroke.path.length === 4 && !stroke.filled && stroke.alpha > 0);
  const signature = () => pending().map((stroke) => stroke.path
    .map(([x, y]) => `${x.toFixed(2)},${y.toFixed(2)}`).join(" ")).join(" | ");

  try {
    await import(`./tower.js?mature-pending=${Date.now()}`);
    assert.equal(pending().length, 3, "boundary height needs a three-face pending cube in overview");
    const overviewBoundary = signature();

    body.dataset.node = "bitcoin";
    step(8);
    assert.equal(pending().length, 3, "boundary height needs a three-face pending cube in focus");
    const focusedBoundary = signature();

    body.dataset.node = "";
    step(8);
    live.dataset.nextBlock = "974310";
    step(35);
    assert.equal(pending().length, 3, "non-boundary height needs a three-face pending cube in overview");
    assert.notEqual(signature(), overviewBoundary, "the pending cube should follow the next height");

    body.dataset.node = "bitcoin";
    step(8);
    assert.equal(pending().length, 3, "non-boundary height needs a three-face pending cube in focus");
    assert.notEqual(signature(), focusedBoundary, "the focused pending cube should follow the next height");

    theme = "light";
    step();
    assert.equal(pending().length, 3, "light mode needs the same unfilled pending cube");
  } finally {
    delete globalThis.document;
    delete globalThis.window;
    delete globalThis.requestAnimationFrame;
  }
});

test("towerLayout stacks (centres the tower) below the wide breakpoint", () => {
  for (const W of [375, 900, 1023]) {  // phones through the awkward mid-range
    const m = towerLayout(W);
    assert.equal(m.wide, false, `W=${W} should stack`);
    assert.equal(m.ox, W / 2);
  }
});

test("towerLayout keeps the tower clear of the card zone on wide screens", () => {
  for (const W of [1024, 1280, 1920]) {
    const { wide, ox, tw } = towerLayout(W, GRID);
    assert.equal(wide, true);
    assert.ok(ox - (GRID * tw) / 2 >= 500, `W=${W} overlaps card`);
    assert.ok(ox + (GRID * tw) / 2 <= W + 1, `W=${W} runs off screen`);
  }
});

test("overview bouquet spreads farther on large screens without enlarging the towers", () => {
  const span = ({ positions }) => Math.max(...positions) - Math.min(...positions);
  const laptop = overviewLayout(1440, 7, 900);
  const desktop = overviewLayout(1920, 7, 900);
  assert.ok(span(desktop) > span(laptop) * 1.2);
  assert.equal(desktop.tw, laptop.tw);
});

test("seven-tower mobile bouquet stays compact, contained, and selectable", () => {
  for (const width of [320, 375, 560]) {
    const layout = overviewLayout(width, 7, 700);
    const hitWidth = overviewHitWidth(layout.tw);
    const sortedAnchors = layout.anchors.slice().sort((a, b) => a - b);
    const minGap = Math.min(...sortedAnchors.slice(1).map((y, index) => y - sortedAnchors[index]));
    const targets = sortedAnchors.map((anchor) => overviewChoiceGeometry(width, anchor, 12 * layout.tw * 1.12, minGap));
    assert.equal(layout.positions.length, 7);
    assert.equal(layout.contentWidth, width);
    assert.ok(hitWidth >= 140);
    assert.ok(layout.positions.every((x) => x - hitWidth / 2 >= 0 && x + hitWidth / 2 <= layout.contentWidth));
    assert.ok(minGap >= 52);
    assert.ok(targets.every(({ height }) => height >= 44));
    assert.ok(targets.slice(1).every(({ top }, index) => top >= targets[index].top + targets[index].height));
  }
});

test("a foreground Tari block label clears the neighboring Monero name card", () => {
  const width = 872;
  const layout = overviewLayout(width, 7, 888);
  const progress = { bitcoin: 100, monero: 62, tari: 33, litecoin: 82, dogecoin: 48, bitcoincash: 94, zcash: 100 };
  const slots = assignOverviewSlots(NODE_IDS, progress, layout);
  const centers = Object.fromEntries(NODE_IDS.map((node) => {
    const slot = slots[node];
    return [node, overviewLabelPosition(slot.x, slot.y, slot.tw).left];
  }));
  const labelWidth = 168;
  const nameCardWidth = 112;
  assert.ok(Math.abs(centers.tari - slots.monero.x) >= (labelWidth + nameCardWidth) / 2);
  assert.ok(Object.values(centers).every((center) => center - labelWidth / 2 >= 0 && center + labelWidth / 2 <= width));
});

test("preview tower reaches the viewport bottom", () => {
  for (const [H, anchorY, tw] of [[667, 200, 14], [900, 270, 40], [2160, 648, 46]]) {
    const depth = previewDepth(H, anchorY, tw);
    for (const [gx, gy] of [[GRID, 0], [0, GRID], [GRID, GRID]]) {
      const bottom = project(gx, gy, 0, tw, tw * 0.5, 0, anchorY - GRID * (tw / 4) + (depth + 1) * tw * 0.5).y;
      assert.ok(bottom > H, `H=${H} corner=${gx},${gy} stops at ${bottom}`);
    }
  }
});

test("zoom keeps at least the overview tower depth throughout the transition", () => {
  const H = 900;
  const overviewY = H * 0.3;
  const overviewTw = 40;
  const initialDepth = previewDepth(H, overviewY, overviewTw);
  for (const progress of [0, 0.25, 0.5, 0.75, 1]) {
    const anchorY = overviewY + (H * 0.6 - overviewY) * progress;
    const tw = overviewTw + (52 - overviewTw) * progress;
    const depth = sceneDepth(H, anchorY, tw, overviewY, overviewTw);
    assert.ok(depth >= initialDepth, `progress=${progress} shrank from ${initialDepth} to ${depth}`);
    for (const [gx, gy] of [[GRID, 0], [0, GRID], [GRID, GRID]]) {
      const bottom = project(gx, gy, 0, tw, tw * 0.5, 0, anchorY - GRID * (tw / 4) + (depth + 1) * tw * 0.5).y;
      assert.ok(bottom > H, `progress=${progress} corner=${gx},${gy} stops at ${bottom}`);
    }
  }
});

test("overview block labels use each live node height only when labels are enabled", () => {
  assert.deepEqual(overviewBlockLabels({ bitcoin: "974001", monero: "3512002" }, true, true), {
    bitcoin: "loading block 974001",
    monero: "loading block 3512002",
  });
  assert.deepEqual(overviewBlockLabels({ bitcoin: "974001", monero: "" }, true, true), {
    bitcoin: "loading block 974001",
    monero: "",
  });
  assert.deepEqual(overviewBlockLabels({ bitcoin: "974001", monero: "3512002" }, false, true), { bitcoin: "", monero: "" });
  assert.deepEqual(overviewBlockLabels({ bitcoin: "974001", monero: "3512002" }, true, false), { bitcoin: "", monero: "" });
});
