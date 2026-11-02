// The table, drawn with PixiJS. World units are inches. The view shows what
// the server has confirmed: dragging a token draws a ghost, and the token
// itself moves only when its token.moved event arrives (ADR-003).

import { Application, Container, Graphics, Sprite, Text, Texture, type FederatedPointerEvent } from "pixi.js";
import { fit, pan, toScreen, toWorld, zoomAt, type Camera, type Point } from "./camera";
import type { Outcome } from "./connection";
import type { Grid, MapLoaded, Pose, State, Token } from "./protocol";
import { distanceIn, formatInches } from "./ruler";

export interface TableOptions {
  /** Asks the server to move a token. The ghost stays until it settles. */
  move(tokenId: string, to: Pose): Promise<Outcome>;
  /** A token was clicked (not dragged), or the background was (null). */
  select(tokenId: string | null): void;
}

const background = 0x1d2023;
const lineColor = 0xe8e6e3;
const rulerColor = 0xffc94d;
const detail = 100; // token bodies are drawn at this many units per inch

export class TableView {
  private readonly app = new Application();
  private readonly world = new Container(); // inches; moved and scaled by the camera
  private readonly mapLayer = new Container();
  private readonly gridLayer = new Graphics();
  private readonly tokenLayer = new Container();
  private readonly ghostLayer = new Container();
  private readonly rulerLine = new Graphics();
  private readonly overlay = new Container(); // screen pixels: labels stay sharp at any zoom
  private readonly rulerLabel = new Text({ text: "", style: { fontSize: 14, fill: rulerColor, fontWeight: "bold" } });

  private camera: Camera = { x: 40, y: 40, scale: 40 };
  private fitted = false;
  private state: State | null = null;
  private mapAsset = "";
  private textures = new Map<string, Promise<Texture>>();
  private labels = new Map<string, Text>();
  private selected: string | null = null;

  private drag:
    | { kind: "token"; token: Token; offset: Point; ghost: Container }
    | { kind: "pan"; last: Point; moved: boolean }
    | { kind: "ruler"; from: Point }
    | null = null;
  rulerMode = false;

  constructor(private readonly opts: TableOptions) {}

  async mount(el: HTMLElement): Promise<void> {
    await this.app.init({ resizeTo: el, background, antialias: true, autoDensity: true, resolution: window.devicePixelRatio });
    el.appendChild(this.app.canvas);

    this.world.addChild(this.mapLayer, this.gridLayer, this.tokenLayer, this.ghostLayer, this.rulerLine);
    this.rulerLabel.visible = false;
    this.overlay.addChild(this.rulerLabel);
    this.app.stage.addChild(this.world, this.overlay);

    const stage = this.app.stage;
    stage.eventMode = "static";
    stage.hitArea = this.app.screen;
    stage.on("pointerdown", (e) => this.pointerDown(e));
    stage.on("globalpointermove", (e) => this.pointerMove(e));
    stage.on("pointerup", () => this.pointerUp());
    stage.on("pointerupoutside", () => this.pointerUp());
    this.app.canvas.addEventListener(
      "wheel",
      (e) => {
        e.preventDefault();
        this.setCamera(zoomAt(this.camera, { x: e.offsetX, y: e.offsetY }, Math.exp(-e.deltaY * 0.0015)));
      },
      { passive: false },
    );
    this.app.renderer.on("resize", () => this.layoutOverlay());
    this.applyCamera();
  }

  destroy(): void {
    this.app.destroy(true, { children: true });
  }

  /** Shows state. Called after the welcome and after every event. */
  setState(state: State): void {
    this.state = state;
    if (!this.fitted && state.map) {
      this.setCamera(fit(state.map.width_in, state.map.height_in, this.app.screen));
      this.fitted = true;
    }
    this.drawMap(state.map);
    this.drawTokens(state.tokens);
    this.layoutOverlay();
  }

  /** Fits the camera to the map, or to the tokens if there is no map. */
  /** Highlights a token, or none. */
  setSelected(tokenId: string | null): void {
    this.selected = tokenId;
    if (this.state) this.drawTokens(this.state.tokens);
  }

  /** Where a world point is on the page, in CSS pixels. */
  toPage(p: Point): Point {
    const rect = this.app.canvas.getBoundingClientRect();
    const s = toScreen(this.camera, p);
    return { x: rect.left + s.x, y: rect.top + s.y };
  }

  /** The world point at the middle of the view, where new tokens go. */
  viewCentre(): Point {
    return toWorld(this.camera, { x: this.app.screen.width / 2, y: this.app.screen.height / 2 });
  }

  recenter(): void {
    const s = this.state;
    if (s?.map) this.setCamera(fit(s.map.width_in, s.map.height_in, this.app.screen));
  }

  private setCamera(c: Camera): void {
    this.camera = c;
    this.applyCamera();
  }

  private applyCamera(): void {
    this.world.position.set(this.camera.x, this.camera.y);
    this.world.scale.set(this.camera.scale);
    this.layoutOverlay();
  }

  private drawMap(map: MapLoaded | null): void {
    const asset = map?.asset ?? "";
    if (asset !== this.mapAsset) {
      this.mapAsset = asset;
      this.mapLayer.removeChildren().forEach((c) => c.destroy());
      if (map) {
        void this.texture(map.asset).then((tex) => {
          if (this.mapAsset !== map.asset) return; // replaced while loading
          const sprite = new Sprite(tex);
          sprite.width = map.width_in;
          sprite.height = map.height_in;
          this.mapLayer.addChild(sprite);
        });
      }
    }
    this.gridLayer.clear();
    if (map) {
      // The map's edge, so an image that has not loaded yet still shows where the table is.
      this.gridLayer.rect(0, 0, map.width_in, map.height_in).stroke({ width: 0.04, color: lineColor, alpha: 0.5 });
      if (map.grid) drawGrid(this.gridLayer, map.grid, map.width_in, map.height_in);
    }
  }

  private drawTokens(tokens: Record<string, Token>): void {
    this.tokenLayer.removeChildren().forEach((c) => c.destroy({ children: true }));
    for (const [id, label] of this.labels) {
      if (!(id in tokens)) {
        label.destroy();
        this.labels.delete(id);
      }
    }
    for (const t of Object.values(tokens)) {
      const piece = this.piece(t, 1);
      if (t.id === this.selected) {
        const ring = new Graphics().circle(0, 0, (t.size_in / 2 + 0.12) * detail).stroke({ width: 0.08 * detail, color: rulerColor });
        ring.scale.set(1 / detail);
        piece.addChildAt(ring, 0);
      }
      piece.eventMode = "static";
      piece.cursor = "grab";
      piece.on("pointerdown", (e) => {
        if (this.rulerMode || e.shiftKey) return; // let the stage start a ruler
        e.stopPropagation();
        const p = toWorld(this.camera, e.global);
        const ghost = this.piece(t, 0.45);
        this.ghostLayer.addChild(ghost);
        this.drag = { kind: "token", token: t, offset: { x: p.x - t.pose.x, y: p.y - t.pose.y }, ghost };
      });
      this.tokenLayer.addChild(piece);

      let label = this.labels.get(t.id);
      if (!label) {
        label = new Text({ text: "", style: { fontSize: 13, fill: lineColor, stroke: { color: background, width: 3 } } });
        label.anchor.set(0.5, 0);
        this.labels.set(t.id, label);
        this.overlay.addChild(label);
      }
      label.text = t.label;
    }
  }

  /** A token's body: a disc in its colour (or its image), with a facing mark. */
  private piece(t: Token, alpha: number): Container {
    const c = new Container({ alpha });
    c.position.set(t.pose.x, t.pose.y);
    c.rotation = (t.pose.rotation * Math.PI) / 180; // clockwise from north, as on screen
    // Drawn in hundredths of an inch and scaled down: PixiJS picks how
    // smooth a circle is from its radius before scaling.
    const body = new Container({ scale: 1 / detail });
    c.addChild(body);
    const r = (t.size_in / 2) * detail;
    body.addChild(new Graphics().circle(0, 0, r).fill(t.color).stroke({ width: 0.05 * detail, color: background }));
    if (t.asset) {
      const mask = new Graphics().circle(0, 0, r).fill(0xffffff);
      body.addChild(mask);
      void this.texture(t.asset).then((tex) => {
        if (c.destroyed) return;
        const img = new Sprite({ texture: tex, anchor: 0.5, width: 2 * r, height: 2 * r });
        img.mask = mask;
        body.addChildAt(img, 1);
      });
    }
    body.addChild(new Graphics().moveTo(0, 0).lineTo(0, -r).stroke({ width: Math.max(0.04 * detail, r * 0.12), color: lineColor }));
    return c;
  }

  private texture(hash: string): Promise<Texture> {
    let tex = this.textures.get(hash);
    if (!tex) {
      // Assets are named by hash with no extension, so load them as images
      // directly rather than through PixiJS's extension-based loader.
      const img = new Image();
      img.src = `/assets/${hash}`;
      tex = img.decode().then(() => Texture.from(img));
      this.textures.set(hash, tex);
    }
    return tex;
  }

  /** Puts screen-space labels under their tokens, and the ruler's label at its end. */
  private layoutOverlay(): void {
    const tokens = this.state?.tokens ?? {};
    for (const [id, label] of this.labels) {
      const t = tokens[id];
      if (!t) continue;
      label.position.set(
        t.pose.x * this.camera.scale + this.camera.x,
        (t.pose.y + t.size_in / 2) * this.camera.scale + this.camera.y + 2,
      );
    }
  }

  private pointerDown(e: FederatedPointerEvent): void {
    const p = toWorld(this.camera, e.global);
    if (this.rulerMode || e.shiftKey) {
      this.drag = { kind: "ruler", from: p };
      this.drawRuler(p, p, e.global);
    } else {
      this.clearRuler();
      this.drag = { kind: "pan", last: { x: e.global.x, y: e.global.y }, moved: false };
    }
  }

  private pointerMove(e: FederatedPointerEvent): void {
    const d = this.drag;
    if (!d) return;
    switch (d.kind) {
      case "token": {
        const p = toWorld(this.camera, e.global);
        d.ghost.position.set(p.x - d.offset.x, p.y - d.offset.y);
        break;
      }
      case "pan":
        d.moved = true;
        this.setCamera(pan(this.camera, e.global.x - d.last.x, e.global.y - d.last.y));
        d.last = { x: e.global.x, y: e.global.y };
        break;
      case "ruler":
        this.drawRuler(d.from, toWorld(this.camera, e.global), e.global);
        break;
    }
  }

  private pointerUp(): void {
    const d = this.drag;
    this.drag = null;
    if (d?.kind === "pan" && !d.moved) this.opts.select(null);
    if (d?.kind !== "token") return;
    const to = { x: d.ghost.x, y: d.ghost.y, rotation: d.token.pose.rotation };
    if (to.x === d.token.pose.x && to.y === d.token.pose.y) {
      d.ghost.destroy({ children: true });
      this.opts.select(d.token.id); // a click, not a drag
      return;
    }
    // The ghost stays where it was dropped until the server answers; the
    // token jumps there when its event arrives.
    void this.opts.move(d.token.id, to).finally(() => d.ghost.destroy({ children: true }));
  }

  private drawRuler(from: Point, to: Point, screen: Point): void {
    const w = 2 / this.camera.scale; // 2 pixels at any zoom
    this.rulerLine
      .clear()
      .moveTo(from.x, from.y)
      .lineTo(to.x, to.y)
      .stroke({ width: w, color: rulerColor })
      .circle(from.x, from.y, 3 * w)
      .fill(rulerColor);
    this.rulerLabel.text = formatInches(distanceIn(from, to));
    this.rulerLabel.position.set(screen.x + 12, screen.y + 12);
    this.rulerLabel.visible = true;
  }

  private clearRuler(): void {
    this.rulerLine.clear();
    this.rulerLabel.visible = false;
  }
}

/** Draws a square or hex grid over a width × height inch map. */
function drawGrid(g: Graphics, grid: Grid, width: number, height: number): void {
  const s = grid.size_in;
  const style = { width: 0.02, color: lineColor, alpha: 0.35 };
  if (grid.kind === "square") {
    const first = (origin: number) => origin - Math.floor(origin / s) * s;
    for (let x = first(grid.origin_x); x <= width; x += s) g.moveTo(x, 0).lineTo(x, height);
    for (let y = first(grid.origin_y); y <= height; y += s) g.moveTo(0, y).lineTo(width, y);
    g.stroke(style);
    return;
  }
  // Pointy-top hexes, size_in across the flats.
  const r = s / Math.sqrt(3);
  const rowStep = 1.5 * r;
  const startRow = Math.floor((0 - grid.origin_y) / rowStep) - 1;
  const endRow = Math.ceil((height - grid.origin_y) / rowStep) + 1;
  for (let row = startRow; row <= endRow; row++) {
    const cy = grid.origin_y + row * rowStep;
    const shift = row % 2 === 0 ? 0 : s / 2;
    const startCol = Math.floor((0 - grid.origin_x - shift) / s) - 1;
    const endCol = Math.ceil((width - grid.origin_x - shift) / s) + 1;
    for (let col = startCol; col <= endCol; col++) {
      const cx = grid.origin_x + shift + col * s;
      const pts: number[] = [];
      for (let i = 0; i < 6; i++) {
        const a = (Math.PI / 3) * i + Math.PI / 6;
        pts.push(cx + r * Math.cos(a), cy + r * Math.sin(a));
      }
      g.poly(pts, true);
    }
  }
  g.stroke(style);
}
