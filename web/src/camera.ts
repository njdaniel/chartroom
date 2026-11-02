// Pan and zoom. The table's world units are inches; the camera says where
// the world's origin is on screen and how many pixels an inch takes.

export interface Point {
  x: number;
  y: number;
}

export interface Camera {
  x: number; // screen position of the world origin, in pixels
  y: number;
  scale: number; // pixels per inch
}

export const MIN_SCALE = 2;
export const MAX_SCALE = 400;

export function toWorld(c: Camera, p: Point): Point {
  return { x: (p.x - c.x) / c.scale, y: (p.y - c.y) / c.scale };
}

export function toScreen(c: Camera, p: Point): Point {
  return { x: p.x * c.scale + c.x, y: p.y * c.scale + c.y };
}

/** Zooms by factor, keeping the world point under screen point p still. */
export function zoomAt(c: Camera, p: Point, factor: number): Camera {
  const scale = Math.min(MAX_SCALE, Math.max(MIN_SCALE, c.scale * factor));
  const w = toWorld(c, p);
  return { scale, x: p.x - w.x * scale, y: p.y - w.y * scale };
}

export function pan(c: Camera, dx: number, dy: number): Camera {
  return { ...c, x: c.x + dx, y: c.y + dy };
}

/** A camera that fits a width × height inch area into a view, with a margin. */
export function fit(widthIn: number, heightIn: number, view: { width: number; height: number }): Camera {
  const margin = 0.9;
  const scale = Math.min(
    MAX_SCALE,
    Math.max(MIN_SCALE, Math.min((view.width * margin) / widthIn, (view.height * margin) / heightIn)),
  );
  return { scale, x: (view.width - widthIn * scale) / 2, y: (view.height - heightIn * scale) / 2 };
}
