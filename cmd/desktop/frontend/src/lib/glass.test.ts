import { describe, expect, it } from "vitest";
import { lensOffset, lensTile, rimWidth, tileSize } from "./glass";

const at = (tile: { width: number; data: Uint8ClampedArray }, x: number, y: number) => {
  const i = (y * tile.width + x) * 4;
  return [tile.data[i], tile.data[i + 1]];
};

describe("rimWidth", () => {
  it("卡片上 44px,窄的表面取短边的 1/3.5,不小于 4px", () => {
    expect(rimWidth(560, 400)).toBe(44);
    expect(rimWidth(420, 42)).toBeCloseTo(12);
    expect(rimWidth(20, 10)).toBe(4);
  });
});

describe("lensOffset", () => {
  it("贴着边缘往里看 1.1 倍镜边宽，到镜边内沿减到 0", () => {
    expect(lensOffset(0, 20)).toBeCloseTo(22);
    expect(lensOffset(20, 20)).toBe(0);
    expect(lensOffset(30, 20)).toBe(0);
    expect(lensOffset(5, 20)).toBeGreaterThan(lensOffset(10, 20));
  });
});

describe("lensTile", () => {
  it("左边条贴边往右推、镜边以内不动，竖直方向不动", () => {
    const tile = lensTile("l", 12, 12);
    const [r, g] = at(tile, 0, 0);
    expect(r).toBeGreaterThan(240);
    expect(g).toBe(128);
    expect(at(tile, tile.width - 1, 0)).toEqual([128, 128]);
  });

  it("右边条和下边条朝反方向推", () => {
    expect(at(lensTile("r", 12, 12), 11, 0)[0]).toBeLessThan(16);
    expect(at(lensTile("b", 12, 12), 0, 11)[1]).toBeLessThan(16);
  });

  it("左上角沿圆角的法线往里推，圆角外面的像素不动", () => {
    const radius = 16;
    const tile = lensTile("tl", radius, 12);
    expect(tile.width).toBe(tileSize(radius, 12));
    // 45° 方向上贴着圆弧的一点：往右下推，两个方向一样多。
    const p = Math.round(radius - radius / Math.SQRT2);
    const [r, g] = at(tile, p, p);
    expect(r).toBeGreaterThan(160);
    expect(Math.abs(r - g)).toBeLessThanOrEqual(1);
    expect(at(tile, 0, 0)).toEqual([128, 128]);
  });

  it("镜边比圆角宽时，角上的块按镜边算", () => {
    expect(tileSize(4, 12)).toBe(12);
    const tile = lensTile("br", 4, 12);
    const [r, g] = at(tile, tile.width - 1, 0);
    expect(r).toBeLessThan(16);
    expect(g).toBe(128);
  });
});
