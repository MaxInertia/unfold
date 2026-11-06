import {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { fetchCity } from "./api";
import {
  buildingAt,
  CELL_W,
  clickTarget,
  clientToWorld,
  districtAt,
  fitViewBox,
  LABEL_H,
  SLOT,
  layoutCity,
  markCity,
  panViewBox,
  tooltipText,
  truncateLabel,
  worldPerPixel,
  zoomViewBox,
  type Building,
  type CityData,
  type CityLayout,
  type ViewBox,
} from "./city";
import { useTourStopTargets, useUnfoldedTargets } from "./cityMarks";
import { useReloadRevision } from "./reload";
import type { Frame } from "./types";

// The city: the repository as districts and buildings, drawn where city.ts
// says and never anywhere else. This file is the camera, the hover card and
// the marks; the layout rule and its stability argument are in city.ts.
//
// One SVG. The base layer — every district and building — is a memoized
// component keyed on the layout alone, so hovering, panning and marking never
// re-render the thousand rectangles underneath: hover is a hit test on the
// container, not a handler per shape, and the marks are a thin overlay.
export function City({
  repo,
  rootFrame,
  onOpen,
  onClose,
  mode,
}: {
  // Which workspace service to draw; null is the repo unfold was pointed at.
  repo: string | null;
  rootFrame: Frame | null;
  onOpen: (id: string) => void;
  onClose: () => void;
  // Beside the code, or floating over a corner of it when the viewport is
  // too narrow for both. The drawing is the same; the frame around it isn't.
  mode: "side" | "overlay";
}) {
  const revision = useReloadRevision();
  const [data, setData] = useState<CityData | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    fetchCity(repo)
      .then((d) => {
        if (!alive) return;
        setData(d);
        setError(null);
      })
      .catch((e: Error) => alive && setError(e.message));
    return () => {
      alive = false;
    };
  }, [repo, revision]);

  const layout = useMemo(() => (data ? layoutCity(data) : null), [data]);
  const unfolded = useUnfoldedTargets(rootFrame);
  const tour = useTourStopTargets();
  const marked = useMemo(
    () =>
      layout
        ? markCity(layout, { root: rootFrame?.id ?? null, unfolded, tour })
        : null,
    [layout, rootFrame?.id, unfolded, tour],
  );

  const svgRef = useRef<SVGSVGElement>(null);
  // The canvas is always mounted; the svg only once there is a viewBox, so the
  // first fit has to measure the frame the svg will fill, not the svg.
  const canvasRef = useRef<HTMLDivElement>(null);
  const [viewBox, setViewBox] = useState<ViewBox | null>(null);
  const [hover, setHover] = useState<{
    b: Building;
    x: number;
    y: number;
    hostW: number;
  } | null>(null);
  const [hoverDistrict, setHoverDistrict] = useState<string | null>(null);
  const tipRef = useRef<HTMLDivElement>(null);
  const [tipLeft, setTipLeft] = useState(0);
  useLayoutEffect(() => {
    if (!hover || !tipRef.current) return;
    const w = tipRef.current.offsetWidth;
    setTipLeft(Math.max(4, Math.min(hover.x + 12, hover.hostW - w - 4)));
  }, [hover]);

  const aspect = () => {
    const r = canvasRef.current?.getBoundingClientRect();
    return r && r.height > 0 ? r.width / r.height : 1;
  };

  // The camera is yours once you've touched it: a reindex redraws buildings
  // in place without yanking the view back to the whole city. Only the first
  // layout, and the fit button, frame everything.
  const fit = useCallback(() => {
    if (layout) setViewBox(fitViewBox(layout.bounds, aspect()));
  }, [layout]);
  useEffect(() => {
    if (layout && !viewBox) fit();
  }, [layout, viewBox, fit]);

  // React registers wheel listeners as passive, so preventDefault — needed or
  // the page scrolls behind the map — has to go through a native listener.
  useEffect(() => {
    const svg = svgRef.current;
    if (!svg || !layout) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      setViewBox((vb) => {
        if (!vb) return vb;
        const p = clientToWorld(
          vb,
          svg.getBoundingClientRect(),
          e.clientX,
          e.clientY,
        );
        return zoomViewBox(
          vb,
          e.deltaY < 0 ? 1.2 : 1 / 1.2,
          p.x,
          p.y,
          layout.bounds,
        );
      });
    };
    svg.addEventListener("wheel", onWheel, { passive: false });
    return () => svg.removeEventListener("wheel", onWheel);
    // The svg mounts only once there is a viewBox, so the listener has to be
    // attached then — not when the layout arrived, when there was no svg yet.
  }, [layout, viewBox !== null]);

  // Drag pans; a press that doesn't travel is a click. The threshold is what
  // separates them, since a click on a 10-unit slot is also a tiny drag.
  const drag = useRef<{ x: number; y: number; moved: boolean } | null>(null);

  const worldAt = (e: { clientX: number; clientY: number }) => {
    const svg = svgRef.current;
    if (!svg || !viewBox) return null;
    return clientToWorld(
      viewBox,
      svg.getBoundingClientRect(),
      e.clientX,
      e.clientY,
    );
  };

  const onPointerDown = (e: React.PointerEvent<SVGSVGElement>) => {
    if (e.button !== 0) return;
    drag.current = { x: e.clientX, y: e.clientY, moved: false };
    e.currentTarget.setPointerCapture(e.pointerId);
  };

  const onPointerMove = (e: React.PointerEvent<SVGSVGElement>) => {
    const svg = svgRef.current;
    if (!svg || !viewBox || !layout) return;
    const d = drag.current;
    if (d && e.buttons & 1) {
      const dx = e.clientX - d.x;
      const dy = e.clientY - d.y;
      if (!d.moved && Math.hypot(dx, dy) < 4) return;
      d.moved = true;
      const k = worldPerPixel(viewBox, svg.getBoundingClientRect());
      setViewBox(panViewBox(viewBox, -dx * k, -dy * k));
      drag.current = { x: e.clientX, y: e.clientY, moved: true };
      setHover(null);
      return;
    }
    const p = worldAt(e);
    if (!p) return;
    const b = buildingAt(layout, p.x, p.y);
    const host = svg.parentElement?.getBoundingClientRect();
    setHover(
      b
        ? {
            b,
            x: e.clientX - (host?.left ?? 0),
            y: e.clientY - (host?.top ?? 0),
            hostW: host?.width ?? 0,
          }
        : null,
    );
    setHoverDistrict(districtAt(layout, p.x, p.y)?.path ?? null);
  };

  const onPointerUp = (e: React.PointerEvent<SVGSVGElement>) => {
    const d = drag.current;
    drag.current = null;
    if (!d || d.moved || !layout) return;
    const p = worldAt(e);
    if (!p) return;
    const b = buildingAt(layout, p.x, p.y);
    if (b) onOpen(clickTarget(b));
  };

  const onPointerLeave = () => {
    setHover(null);
    setHoverDistrict(null);
  };

  // Escape closes the floating map, the way it closes everything else that
  // floats. Beside the code it is a panel, and panels don't vanish on escape.
  useEffect(() => {
    if (mode !== "overlay") return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mode, onClose]);

  const functions = layout
    ? layout.districts.reduce((n, d) => n + d.count, 0)
    : 0;
  const moduleShort =
    layout?.module.slice(layout.module.lastIndexOf("/") + 1) || layout?.module;

  return (
    <div className={`city city--${mode}`} data-testid="city">
      <div className="city-header">
        <span className="city-title" title={layout?.module}>
          city{moduleShort ? ` · ${moduleShort}` : ""}
        </span>
        {layout && (
          <span className="city-counts">
            {layout.districts.length} packages · {functions} functions
          </span>
        )}
        <span className="city-spacer" />
        {marked?.root && (
          <button
            type="button"
            className="city-btn"
            title={`center on ${marked.root.name}`}
            onClick={() => {
              const r = marked.root!;
              setViewBox((vb) =>
                vb ? { ...vb, x: r.cx - vb.w / 2, y: r.cy - vb.h / 2 } : vb,
              );
            }}
          >
            ⌖
          </button>
        )}
        <button
          type="button"
          className="city-btn"
          title="fit the whole city"
          onClick={fit}
        >
          fit
        </button>
        <button
          type="button"
          className="city-btn"
          title="hide the city (c)"
          onClick={onClose}
          aria-label="hide city"
        >
          ×
        </button>
      </div>

      <div className="city-canvas" ref={canvasRef}>
        {error && <div className="city-error">{error}</div>}
        {!layout && !error && <div className="city-loading">loading city…</div>}
        {layout && viewBox && marked && (
          <svg
            ref={svgRef}
            className={`city-svg${drag.current?.moved ? " city-svg--dragging" : ""}`}
            viewBox={`${viewBox.x} ${viewBox.y} ${viewBox.w} ${viewBox.h}`}
            preserveAspectRatio="xMidYMid meet"
            onPointerDown={onPointerDown}
            onPointerMove={onPointerMove}
            onPointerUp={onPointerUp}
            onPointerLeave={onPointerLeave}
            role="img"
            aria-label={`city map of ${layout.module}`}
          >
            <CityBase layout={layout} />
            {/* The marks, thin on top of the base. Links first so every
                building draws over them. */}
            <g className="city-marks">
              {hoverDistrict && (
                <DistrictHalo layout={layout} path={hoverDistrict} />
              )}
              {/* Root and callees are marked on their whole slot with a
                  screen-pixel stroke: at the whole-city fit a building is a
                  few pixels, and a mark that scaled with it would vanish
                  exactly when you need it to find where you are. */}
              {marked.links.map((l) => (
                <line
                  key={l.to.id}
                  className="city-link"
                  vectorEffect="non-scaling-stroke"
                  x1={l.from.cx}
                  y1={l.from.cy}
                  x2={l.to.cx}
                  y2={l.to.cy}
                />
              ))}
              {marked.lit.map((b) => (
                <rect
                  key={b.id}
                  className="city-lit"
                  vectorEffect="non-scaling-stroke"
                  x={b.slot.x}
                  y={b.slot.y}
                  width={SLOT}
                  height={SLOT}
                  rx={0.8}
                />
              ))}
              {marked.root && (
                <rect
                  className="city-root"
                  vectorEffect="non-scaling-stroke"
                  x={marked.root.slot.x}
                  y={marked.root.slot.y}
                  width={SLOT}
                  height={SLOT}
                  rx={0.8}
                />
              )}
              {marked.stops.map((s) => (
                <g
                  key={s.building.id}
                  className="city-stop"
                  transform={`translate(${s.building.cx} ${s.building.slot.y - 1})`}
                >
                  <circle r={2.6} />
                  <text y={0.9} textAnchor="middle">
                    {s.numbers.join(",")}
                  </text>
                </g>
              ))}
              {hover && (
                <rect
                  className="city-hover"
                  vectorEffect="non-scaling-stroke"
                  x={hover.b.slot.x}
                  y={hover.b.slot.y}
                  width={SLOT}
                  height={SLOT}
                  rx={0.8}
                />
              )}
            </g>
          </svg>
        )}
        {hover && (
          <div
            ref={tipRef}
            className="city-tooltip"
            // Beside the cursor, pulled back inside the panel when the name is
            // long enough to run off its edge; measured after render, before
            // paint, so it never shows clipped.
            style={{ left: tipLeft, top: hover.y + 12 }}
            data-testid="city-tooltip"
          >
            <div className="city-tooltip-main">{tooltipText(hover.b)}</div>
            <div className="city-tooltip-sub">
              {hover.b.file.slice(hover.b.file.lastIndexOf("/") + 1)}
              {hover.b.kind === "func" &&
                ` · ${hover.b.callees} callee${hover.b.callees === 1 ? "" : "s"}`}
              {hover.b.changed && " · changed"}
            </div>
          </div>
        )}
      </div>

      <div className="city-legend">
        <span>height = lines</span>
        <span>width = callers</span>
        <span>
          <i className="city-swatch city-swatch--root" /> root
        </span>
        <span>
          <i className="city-swatch city-swatch--lit" /> unfolded
        </span>
        <span>
          <i className="city-swatch city-swatch--stop" /> tour stop
        </span>
        <span>
          <i className="city-swatch city-swatch--changed" /> changed
        </span>
      </div>
    </div>
  );
}

// Every district and building. Memoized on the layout: nothing about hover,
// pan or marks is in here, so it renders once per index build.
const CityBase = memo(function CityBase({ layout }: { layout: CityLayout }) {
  return (
    <g className="city-base">
      {layout.districts.map((d) => (
        <g key={d.path} className="city-district">
          <rect
            className="city-district-box"
            x={d.x}
            y={d.y}
            width={d.w}
            height={d.h}
            rx={2}
          />
          <text
            className="city-district-label"
            x={d.x + 3}
            y={d.y + LABEL_H - 1.5}
          >
            {truncateLabel(d.label, 18)}
            <title>
              {d.path} · {d.count} functions
              {d.mode === "files" ? " (shown by file)" : ""}
            </title>
          </text>
          {d.buildings.map((b) => (
            <rect
              key={b.id}
              className={`city-b${b.kind === "file" ? " city-b--file" : ""}${b.changed ? " city-b--changed" : ""}`}
              x={b.x}
              y={b.y}
              width={b.w}
              height={b.h}
            />
          ))}
        </g>
      ))}
    </g>
  );
});

function DistrictHalo({ layout, path }: { layout: CityLayout; path: string }) {
  const d = layout.districts.find((x) => x.path === path);
  if (!d) return null;
  return (
    <rect
      className="city-district-halo"
      x={d.x - 1}
      y={d.y - 1}
      width={d.w + 2}
      height={d.h + 2}
      rx={2.5}
    />
  );
}

// Exported for the panel's default width: a district and a half, so the
// whole city of a typical repo fits without zooming.
export const CITY_PANEL_WIDTH = Math.round(CELL_W * 3.4);
