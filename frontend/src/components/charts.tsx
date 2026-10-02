import { useEffect, useMemo, useRef, useState } from "preact/hooks";

/*
 * Charts, drawn as SVG: thin 2px lines, a ~10% area wash for a single
 * series, hairline solid gridlines, one y axis, and a crosshair tooltip.
 * Series colours are validated against the black surface (see
 * SERIES_COLORS); text always uses text colours, never the series colour.
 */

/** Categorical slots: download, upload. Validated (dark, #000 surface). */
export const SERIES_COLORS = ["#2563ff", "#b65ec9"];

export interface Series {
  label: string;
  color: string;
  /** One value per x; null leaves a gap. */
  values: (number | null)[];
}

function useWidth<T extends HTMLElement>(): [preact.RefObject<T>, number] {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.floor(e.contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width];
}

/** A clean maximum and tick step for a 0-based axis. */
export function niceScale(max: number, ticks = 4): { max: number; step: number } {
  if (!(max > 0)) return { max: 1, step: 0.25 };
  const raw = max / ticks;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? 10 * mag;
  return { max: Math.ceil(max / step) * step, step };
}

function timeLabel(t: number, spanMs: number): string {
  const d = new Date(t);
  if (spanMs <= 36 * 3600_000) {
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleDateString([], { day: "numeric", month: "short" });
}

function fullTime(t: number): string {
  return new Date(t).toLocaleString([], {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function fmtTick(v: number): string {
  return v >= 1000 ? v.toLocaleString() : String(Math.round(v * 100) / 100);
}

export function LineChart(props: {
  times: number[];
  series: Series[];
  unit: string;
  height?: number;
  format?: (v: number) => string;
  /** Marks under the plot: e.g. failed checks. Values 0–1 are the share failed. */
  strip?: { values: number[]; label: string };
  /** Extra tooltip line per x. */
  note?: (i: number) => string | undefined;
  empty?: string;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const height = props.height ?? 220;
  const fmt = props.format ?? ((v: number) => `${fmtTick(v)} ${props.unit}`);
  const n = props.times.length;
  const pad = { l: 48, r: 12, t: 12, b: 26 };
  const stripH = props.strip ? 10 : 0;
  const plotW = Math.max(10, width - pad.l - pad.r);
  const plotH = height - pad.t - pad.b - (stripH ? stripH + 6 : 0);

  const geo = useMemo(() => {
    if (!n) return null;
    const t0 = props.times[0];
    const t1 = props.times[n - 1];
    const spanMs = Math.max(1, t1 - t0);
    let vmax = 0;
    for (const s of props.series) for (const v of s.values) if (v !== null && v > vmax) vmax = v;
    const { max, step } = niceScale(vmax);
    const x = (t: number) => pad.l + (n === 1 ? plotW / 2 : ((t - t0) / spanMs) * plotW);
    const y = (v: number) => pad.t + plotH - (v / max) * plotH;
    const ticks: number[] = [];
    for (let v = 0; v <= max + step / 2; v += step) ticks.push(v);
    const xTicks: number[] = [];
    const count = Math.max(2, Math.min(6, Math.floor(plotW / 110)));
    for (let i = 0; i < count; i++) xTicks.push(t0 + (spanMs * i) / (count - 1));
    const paths = props.series.map((s) => {
      let d = "";
      let open = false;
      s.values.forEach((v, i) => {
        if (v === null) {
          open = false;
          return;
        }
        d += `${open ? "L" : "M"}${x(props.times[i]).toFixed(1)},${y(v).toFixed(1)}`;
        open = true;
      });
      return d;
    });
    return { x, y, ticks, xTicks, paths, spanMs };
  }, [props.times, props.series, width, height]);

  if (!n) return <div class="chart-empty">{props.empty ?? "No data yet"}</div>;

  const onMove = (e: PointerEvent) => {
    if (!geo) return;
    const rect = (e.currentTarget as SVGElement).getBoundingClientRect();
    const px = e.clientX - rect.left;
    let best = 0;
    let bestD = Infinity;
    for (let i = 0; i < n; i++) {
      const d = Math.abs(geo.x(props.times[i]) - px);
      if (d < bestD) {
        bestD = d;
        best = i;
      }
    }
    setHover(best);
  };

  const single = props.series.length === 1;
  const hx = hover !== null && geo ? geo.x(props.times[hover]) : 0;
  return (
    <div class="chart" ref={ref}>
      {width > 0 && geo && (
        <svg
          width={width}
          height={height}
          role="img"
          aria-label={`${props.series.map((s) => s.label).join(" and ")} over time`}
          onPointerMove={onMove}
          onPointerLeave={() => setHover(null)}
        >
          {geo.ticks.map((v) => (
            <g key={v}>
              <line class="grid" x1={pad.l} x2={pad.l + plotW} y1={geo.y(v)} y2={geo.y(v)} />
              <text class="axis" x={pad.l - 8} y={geo.y(v) + 4} text-anchor="end">
                {fmtTick(v)}
              </text>
            </g>
          ))}
          {geo.xTicks.map((t, i) => (
            <text
              key={t}
              class="axis"
              x={geo.x(t)}
              y={height - 8}
              text-anchor={i === 0 ? "start" : i === geo.xTicks.length - 1 ? "end" : "middle"}
            >
              {timeLabel(t, geo.spanMs)}
            </text>
          ))}
          {single &&
            areaPaths(props.series[0].values, props.times, geo.x, geo.y, pad.t + plotH).map(
              (d, i) => <path key={i} d={d} fill={props.series[0].color} fill-opacity="0.1" />,
            )}
          {props.series.map((s, i) => (
            <path
              key={s.label}
              d={geo.paths[i]}
              fill="none"
              stroke={s.color}
              stroke-width="2"
              stroke-linejoin="round"
              stroke-linecap="round"
            />
          ))}
          {props.strip && (
            <g>
              {props.strip.values.map((v, i) =>
                v > 0 ? (
                  <rect
                    key={i}
                    class="strip-fail"
                    x={geo.x(props.times[i]) - 1.5}
                    y={pad.t + plotH + 6}
                    width={3}
                    height={stripH}
                    opacity={0.4 + 0.6 * v}
                  />
                ) : null,
              )}
            </g>
          )}
          {hover !== null && (
            <g class="crosshair">
              <line x1={hx} x2={hx} y1={pad.t} y2={pad.t + plotH} />
              {props.series.map((s) => {
                const v = s.values[hover];
                return v === null ? null : (
                  <circle
                    key={s.label}
                    cx={hx}
                    cy={geo.y(v)}
                    r={4}
                    fill={s.color}
                    stroke="#000"
                    stroke-width="2"
                  />
                );
              })}
            </g>
          )}
        </svg>
      )}
      {hover !== null && geo && (
        <div
          class="tooltip"
          style={{
            left: `${Math.min(Math.max(hx, 90), width - 90)}px`,
            top: "4px",
          }}
        >
          <div class="tooltip-time">{fullTime(props.times[hover])}</div>
          {props.series.map((s) => (
            <div class="tooltip-row" key={s.label}>
              <span class="swatch" style={{ background: s.color }} />
              <span>{s.label}</span>
              <strong>{s.values[hover] === null ? "—" : fmt(s.values[hover]!)}</strong>
            </div>
          ))}
          {props.strip && props.strip.values[hover] > 0 && (
            <div class="tooltip-row tone-bad">{props.strip.label}</div>
          )}
          {props.note?.(hover) && <div class="tooltip-note">{props.note(hover)}</div>}
        </div>
      )}
    </div>
  );
}

function areaPaths(
  values: (number | null)[],
  times: number[],
  x: (t: number) => number,
  y: (v: number) => number,
  base: number,
): string[] {
  const out: string[] = [];
  let run: [number, number][] = [];
  const flush = () => {
    if (run.length > 1) {
      const top = run.map(([px, py], i) => `${i ? "L" : "M"}${px.toFixed(1)},${py.toFixed(1)}`);
      out.push(
        `${top.join("")}L${run[run.length - 1][0].toFixed(1)},${base}L${run[0][0].toFixed(1)},${base}Z`,
      );
    }
    run = [];
  };
  values.forEach((v, i) => {
    if (v === null) flush();
    else run.push([x(times[i]), y(v)]);
  });
  flush();
  return out;
}

/** A legend for two or more series. */
export function Legend(props: { series: { label: string; color: string; value?: string }[] }) {
  return (
    <div class="legend">
      {props.series.map((s) => (
        <span key={s.label} class="legend-item">
          <span class="swatch" style={{ background: s.color }} />
          {s.label}
          {s.value && <strong>{s.value}</strong>}
        </span>
      ))}
    </div>
  );
}

/** Latest latencies as a small line; failures show as red ticks. */
export function Sparkline(props: { values: number[]; width?: number; height?: number }) {
  const w = props.width ?? 120;
  const h = props.height ?? 28;
  const vals = props.values;
  if (vals.length < 2) return <svg class="spark" width={w} height={h} aria-hidden="true" />;
  const max = Math.max(1, ...vals);
  const step = w / (vals.length - 1);
  let d = "";
  let open = false;
  vals.forEach((v, i) => {
    if (v < 0) {
      open = false;
      return;
    }
    d += `${open ? "L" : "M"}${(i * step).toFixed(1)},${(h - 4 - (v / max) * (h - 8)).toFixed(1)}`;
    open = true;
  });
  return (
    <svg class="spark" width={w} height={h} aria-hidden="true">
      <path d={d} fill="none" stroke="var(--accent)" stroke-width="1.5" stroke-linejoin="round" />
      {vals.map((v, i) =>
        v < 0 ? (
          <rect key={i} class="strip-fail" x={i * step - 1} y={h - 6} width={2} height={6} />
        ) : null,
      )}
    </svg>
  );
}

/** One cell per period (day or hour), coloured by uptime, with a tooltip. */
export function UptimeBars(props: { cells: { label: string; value: number; note?: string }[] }) {
  const [hover, setHover] = useState<number | null>(null);
  return (
    <div class="uptime-bars" onPointerLeave={() => setHover(null)}>
      {props.cells.map((c, i) => (
        <span
          key={i}
          class={`uptime-cell ${cellTone(c.value)}`}
          onPointerEnter={() => setHover(i)}
          aria-label={`${c.label}: ${c.value < 0 ? "no data" : `${c.value.toFixed(2)}% up`}`}
        />
      ))}
      {hover !== null && (
        <div
          class="tooltip"
          style={{ left: `${((hover + 0.5) / props.cells.length) * 100}%`, top: "-6px" }}
        >
          <div class="tooltip-time">{props.cells[hover].label}</div>
          <div class="tooltip-row">
            <strong>
              {props.cells[hover].value < 0
                ? "No data"
                : `${props.cells[hover].value.toFixed(2)}% up`}
            </strong>
          </div>
          {props.cells[hover].note && <div class="tooltip-note">{props.cells[hover].note}</div>}
        </div>
      )}
    </div>
  );
}

function cellTone(v: number): string {
  if (v < 0) return "none";
  if (v >= 99.5) return "good";
  if (v >= 95) return "warn";
  return "bad";
}
