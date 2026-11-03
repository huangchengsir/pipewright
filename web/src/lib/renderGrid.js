/* Render login canvas extraction.
 * Source: dashboard.render.com/assets/CallbackShell-B4Mmpyfi.js, accessed 2026-11-03.
 * Upstream animation core retained; ES module adapter added for this application.
 * Rights remain with their respective owners.
 */
/* eslint-disable -- Preserve the extracted third-party rendering core. */
const a = (value) => {
  throw new Error('Unexpected animation value: ' + value)
}
const A = {
  minViewportPx: 1024,
  cellPx: 32,
  seed: 6292,
  corners: { sizePx: 522, cutAmount: 0.71 },
  sides: { widthPx: 334, jitterCells: 5, smoothness: 7, minGapPx: 32 },
  ditherDepthCells: 3,
  protectedSpacePx: 24,
  protectedTopRadiusPx: 128,
  clearSpace: { paddingPx: 76, radiusPx: 212 },
  motion: {
    waveMs: 200,
    waveJitter: 0.12,
    contentWaveShare: 0.5,
    shimmer: { stepMs: 610, jitter: 0.2, intervalMs: 1900, amount: 0.5 },
    extrude: {
      radiusCells: 1,
      heightPx: 6,
      riseMs: 0,
      fallMs: 200,
      easing: `ease-out`,
      sideOpacity: 0.6,
    },
    press: {
      downMs: 90,
      holdMs: 60,
      upMs: 380,
      rippleCellsPerMs: 0.014,
      rippleReachCells: 7,
      rippleWidthCells: 1.1,
      rippleLift: 0.9,
    },
    sparkle: {
      durationMs: 600,
      frameMs: 70,
      chance: 0.4,
      settleAt: 0.5,
      settleWaveShare: 0.3,
    },
    breathe: {
      depthCells: 4,
      inMs: 500,
      holdMs: 0,
      outMs: 1e3,
      easing: `ease-in-out`,
    },
    shake: { stepsCells: [-1, 1, -1, 1, 0], stepMs: 65 },
    fillIn: {
      spreadMs: 350,
      riseMs: 220,
      holdMs: 350,
      fallMs: 800,
      tintOpacity: 0.05,
      riseFromScale: 0.7,
      riseFromBelow: 0.35,
      opacitySteps: 16,
    },
  },
}
const ve = (e, t, n) => {
    let r = Math.max(1, Math.round(e / n)),
      i = Math.max(1, Math.round(t / n))
    return {
      width: e,
      height: t,
      columns: r,
      rows: i,
      cellWidth: e / r,
      cellHeight: t / i,
    }
  },
  U = (...e) => {
    let t = 2166136261
    for (let n of e) ((t = Math.imul(t ^ (n | 0), 16777619)), (t ^= t >>> 13))
    return (
      (t = Math.imul(t ^ (t >>> 16), 2246822507)),
      (t ^= t >>> 13),
      (t >>> 0) / 4294967296
    )
  },
  ye = (e, t, ...n) => {
    let r = Math.floor(e / t),
      i = (e - r * t) / t,
      a = (1 - Math.cos(i * Math.PI)) / 2,
      o = U(r, ...n) * 2 - 1
    return o + (U(r + 1, ...n) * 2 - 1 - o) * a
  },
  be = (e, t) => ((t % e.columns) + 0.5) * e.cellWidth,
  W = (e, t) => (Math.floor(t / e.columns) + 0.5) * e.cellHeight,
  xe = (e, t, n) => {
    let r = Math.floor(t / e.cellWidth),
      i = Math.floor(n / e.cellHeight)
    return r < 0 || i < 0 || r >= e.columns || i >= e.rows
      ? null
      : { index: i * e.columns + r, column: r, row: i }
  },
  G = (e, t, n, r) => {
    let i = (n.right - n.left) / 2,
      a = (n.bottom - n.top) / 2,
      o = Math.max(0, Math.min(r, i, a)),
      s = Math.abs(e - (n.left + i)) - i + o,
      c = Math.abs(t - (n.top + a)) - a + o
    return (
      Math.hypot(Math.max(s, 0), Math.max(c, 0)) +
      Math.min(Math.max(s, c), 0) -
      o
    )
  },
  Se = (e) =>
    e === `primary`
      ? [
          { right: !0, bottom: !1 },
          { right: !1, bottom: !0 },
        ]
      : [
          { right: !1, bottom: !1 },
          { right: !0, bottom: !0 },
        ],
  Ce = (e, t, n) => {
    let { columns: r, rows: i, cellWidth: a, cellHeight: o } = e,
      s = new Uint8Array(r * i),
      c = (a + o) / 2,
      l = Math.max(2, Math.round(t.sizePx / c)),
      u = (0.2 + t.cutAmount * 0.8) * l
    for (let e of Se(n))
      for (let t = 0; t < Math.min(l, r); t++)
        for (let n = 0; n < Math.min(l, i); n++) {
          let a = t + 0.5,
            o = n + 0.5
          if (Math.hypot(l - a, l - o) < u) continue
          let c = e.right ? r - 1 - t : t,
            d = e.bottom ? i - 1 - n : n
          s[d * r + c] = 1
        }
    return s
  },
  we = (e, t, n, r) => {
    let { columns: i, rows: a, cellWidth: o } = e,
      s = new Uint8Array(i * a),
      c = t.widthPx / o,
      l = Math.max(o, t.minGapPx),
      u = r.length > 0,
      d = Math.min(...r.map((e) => e.left)),
      f = Math.max(...r.map((e) => e.right)),
      p = {
        left: u ? Math.floor((d - l) / o) : i,
        right: u ? Math.floor((e.width - f - l) / o) : i,
      }
    for (let [e, r] of [!1, !0].entries()) {
      let o = Math.max(0, r ? p.right : p.left)
      for (let l = 0; l < a; l++) {
        let a = t.jitterCells * ye(l, t.smoothness, n, e),
          u = Math.min(i, o, Math.max(1, Math.round(c + a)))
        for (let e = 0; e < u; e++) {
          let t = r ? i - 1 - e : e
          s[l * i + t] = 1
        }
      }
    }
    return s
  },
  K = (e, t, n) => {
    let { columns: r, rows: i } = t,
      a = new Float32Array(r * i)
    for (let t = 0; t < e.length; t++) a[t] = e[t] === n ? 0 : 1 / 0
    let o = (e, t, n, o) => {
      if (t < 0 || n < 0 || t >= r || n >= i) return
      let s = a[n * r + t] + o
      s < a[e] && (a[e] = s)
    }
    for (let e = 0; e < i; e++)
      for (let t = 0; t < r; t++) {
        let n = e * r + t
        ;(o(n, t - 1, e, 1),
          o(n, t, e - 1, 1),
          o(n, t - 1, e - 1, Math.SQRT2),
          o(n, t + 1, e - 1, Math.SQRT2))
      }
    for (let e = i - 1; e >= 0; e--)
      for (let t = r - 1; t >= 0; t--) {
        let n = e * r + t
        ;(o(n, t + 1, e, 1),
          o(n, t, e + 1, 1),
          o(n, t + 1, e + 1, Math.SQRT2),
          o(n, t - 1, e + 1, Math.SQRT2))
      }
    return a
  },
  Te = (e, t, n, r) => {
    let { columns: i } = t,
      a = K(e, t, 0)
    return e.map((e, t) => {
      let o = a[t] / (n + 1)
      return e && o < 1 && U(t % i, Math.floor(t / i), r(t)) >= o ? 0 : e
    })
  },
  Ee = (e, t, n, r) => {
    let { columns: i, rows: a, cellWidth: o, cellHeight: s } = e,
      c = t.map((e) => ({
        left: e.left - n - o / 2,
        top: e.top - n - s / 2,
        right: e.right + n + o / 2,
        bottom: e.bottom + n + s / 2,
      })),
      l = new Uint8Array(i * a)
    if (c.length === 0) return l
    for (let t = 0; t < l.length; t++) {
      let n = be(e, t),
        i = W(e, t)
      c.some((e) => G(n, i, e, r) < 0) && (l[t] = 1)
    }
    return l
  },
  De = (e, t, n, r) => {
    let i = K(e, t, 1)
    return e.map((e, t) => (e || (i[t] <= n + 0.01 && !r[t]) ? 1 : 0))
  },
  Oe = (e, t, n) => {
    let { columns: r } = t
    return e.map((t, i) => {
      let a = Math.floor(i / r),
        o = Math.min(r - 1, Math.max(0, (i % r) - n))
      return e[a * r + o]
    })
  },
  ke = (e, t) => {
    let { columns: n, rows: r } = t,
      i = new Uint8Array(e.length)
    for (let t = 0; t < r; t++)
      for (let a = 0; a < n; a++) {
        let o = t * n + a,
          s = (t, i) =>
            t >= 0 && i >= 0 && t < n && i < r && e[i * n + t] !== e[o]
        ;(s(a - 1, t) || s(a + 1, t) || s(a, t - 1) || s(a, t + 1)) &&
          (i[o] = 1)
      }
    return i
  },
  Ae = (e, t, n, r) => {
    let { columns: i, rows: a, cellWidth: o, cellHeight: s } = e,
      c = t.left - n - o / 2,
      l = t.top - n - s / 2,
      u = t.right + n + o / 2,
      d = t.bottom + n + s / 2,
      f = Math.max(0, Math.min(r, (u - c) / 2)),
      p = new Uint8Array(i * a)
    for (let t = 0; t < p.length; t++) {
      let n = be(e, t),
        r = W(e, t)
      n <= c ||
        n >= u ||
        r <= l ||
        r >= d ||
        (r < l + f &&
          ((n < c + f && Math.hypot(n - c - f, r - l - f) > f) ||
            (n > u - f && Math.hypot(n - u + f, r - l - f) > f))) ||
        (p[t] = 1)
    }
    return p
  },
  je = (e, t) => {
    let n =
        t.length > 0
          ? t
          : [
              {
                left: e.width / 2,
                top: e.height / 2,
                right: e.width / 2,
                bottom: e.height / 2,
              },
            ],
      r = new Float32Array(e.columns * e.rows)
    for (let t = 0; t < r.length; t++) {
      let i = be(e, t),
        a = W(e, t),
        o = 1 / 0
      for (let e of n) o = Math.min(o, Math.max(0, G(i, a, e, 0)))
      r[t] = o
    }
    return r
  },
  Me = (e) => {
    let { width: t, height: n } = e,
      r = Math.hypot(t / 2, n / 2) || 1,
      i = new Float32Array(e.columns * e.rows)
    for (let a = 0; a < i.length; a++) {
      let o = be(e, a),
        s = W(e, a)
      i[a] = Math.hypot(Math.min(o, t - o), Math.min(s, n - s)) / r
    }
    return i
  },
  Ne = ({
    lattice: e,
    design: t,
    coverage: n,
    diagonal: r,
    seed: i,
    contentRects: a,
    protectedMask: o,
    ditherSeedAt: s,
    shift: c,
    grow: l,
  }) => {
    let u = Te(
        n === `corners` ? Ce(e, t.corners, r) : we(e, t.sides, i, a),
        e,
        t.ditherDepthCells,
        s,
      ),
      d = (c === 0 ? u : Oe(u, e, c)).map((e, t) => (o[t] ? 0 : e))
    return l > 0 ? De(d, e, l, o) : d
  },
  { extrude: Pe, fillIn: q } = A.motion,
  Fe = (e) => {
    let t = [...e].sort((e, t) => e[0] - t[0] || e[1] - t[1]),
      n = (e, t, n) =>
        (t[0] - e[0]) * (n[1] - e[1]) - (t[1] - e[1]) * (n[0] - e[0]),
      r = (e) => {
        let t = []
        for (let r of e) {
          for (; t.length >= 2 && n(t[t.length - 2], t[t.length - 1], r) <= 0; )
            t.pop()
          t.push(r)
        }
        return t.slice(0, -1)
      }
    return [...r(t), ...r([...t].reverse())]
  },
  Ie = (
    e,
    t,
    {
      lattice: n,
      lineColor: r,
      pixelRatio: i,
      shown: a,
      washed: o,
      rising: s,
      lifted: c,
    },
  ) => {
    let {
        columns: l,
        rows: u,
        cellWidth: d,
        cellHeight: f,
        width: p,
        height: m,
      } = n,
      h = Math.ceil(p * i),
      g = Math.ceil(m * i)
    if (
      ((t.width !== h || t.height !== g) &&
        ((t.width = h),
        (t.height = g),
        (t.style.width = `${p}px`),
        (t.style.height = `${m}px`)),
      e.setTransform(i, 0, 0, i, 0, 0),
      e.clearRect(0, 0, p, m),
      !r)
    )
      return
    let _ = Math.round(i) % 2 == 1 ? 0.5 / i : 0,
      v = (e) => Math.round(e * i) / i,
      y = (e) => v(e * d) + _,
      b = (e) => v(e * f) + _,
      x = (e, t) => e >= 0 && t >= 0 && e < l && t < u && a[t * l + e] === 1
    if (
      ((e.fillStyle = r), (e.strokeStyle = r), (e.lineWidth = 1), s.length > 0)
    ) {
      let t = q.opacitySteps,
        n = new Map()
      for (let { index: e, amount: r } of s) {
        let i = y(e % l),
          a = b(Math.floor(e / l)),
          o = q.riseFromScale + (1 - q.riseFromScale) * r,
          s = d * o,
          c = f * o,
          u = i + d / 2,
          p = a + f / 2 + (1 - r) * f * q.riseFromBelow,
          m = Math.max(1, Math.round(r * t)),
          h = n.get(m) ?? new Path2D()
        ;(h.rect(u - s / 2, p - c / 2, s, c), n.set(m, h))
      }
      for (let [r, i] of n) ((e.globalAlpha = r / t), e.stroke(i))
      e.globalAlpha = 1
      let r = new Path2D()
      for (let e = 0; e < a.length; e++)
        a[e] === 1 && r.rect(y(e % l), b(Math.floor(e / l)), d, f)
      ;((e.globalCompositeOperation = `destination-out`),
        e.fill(r),
        (e.globalCompositeOperation = `source-over`))
    }
    for (let { index: t, amount: n } of o)
      ((e.globalAlpha = q.tintOpacity * n),
        e.fillRect((t % l) * d, Math.floor(t / l) * f, d, f))
    ;((e.globalAlpha = 1), e.beginPath())
    for (let t = 0; t <= u; t++) {
      let n = -1
      for (let r = 0; r <= l; r++) {
        let i = r < l && (x(r, t - 1) || x(r, t))
        i && n < 0
          ? (n = r)
          : !i &&
            n >= 0 &&
            (e.moveTo(y(n), b(t)), e.lineTo(y(r), b(t)), (n = -1))
      }
    }
    for (let t = 0; t <= l; t++) {
      let n = -1
      for (let r = 0; r <= u; r++) {
        let i = r < u && (x(t - 1, r) || x(t, r))
        i && n < 0
          ? (n = r)
          : !i &&
            n >= 0 &&
            (e.moveTo(y(t), b(n)), e.lineTo(y(t), b(r)), (n = -1))
      }
    }
    if ((e.stroke(), c.length === 0)) return
    let ee = Math.min(Pe.heightPx, d / 2),
      S = c
        .map(({ index: e, amount: t }) => {
          let n = e % l,
            r = Math.floor(e / l),
            i = y(n),
            a = b(r),
            o = y(n + 1),
            s = b(r + 1),
            c = (i + o) / 2 - p / 2,
            u = (a + s) / 2 - m / 2,
            d = Math.hypot(c, u) || 1,
            f = t * ee * Math.SQRT2
          return {
            amount: t,
            x0: i,
            y0: a,
            x1: o,
            y1: s,
            dx: v((c / d) * f),
            dy: v((u / d) * f),
          }
        })
        .sort((e, t) => e.amount - t.amount)
    for (let { amount: t, x0: n, y0: r, x1: i, y1: a, dx: o, dy: s } of S) {
      let c = Fe([
        [n, r],
        [i, r],
        [i, a],
        [n, a],
        [n + o, r + s],
        [i + o, r + s],
        [i + o, a + s],
        [n + o, a + s],
      ])
      ;(e.beginPath(),
        c.forEach(([t, n], r) => {
          r === 0 ? e.moveTo(t, n) : e.lineTo(t, n)
        }),
        e.closePath())
      let l = Math.min(1, t)
      ;((e.globalAlpha = Pe.sideOpacity * l),
        e.fill(),
        (e.globalAlpha = l),
        e.stroke())
    }
    e.globalAlpha = 1
    for (let { x0: t, y0: n, x1: r, y1: i, dx: a, dy: o } of S)
      (e.clearRect(t + a, n + o, r - t, i - n),
        e.strokeRect(t + a, n + o, r - t, i - n))
  },
  J = A.motion,
  Y = (e) => Math.min(1, Math.max(0, e)),
  X = (e) => {
    let t = Y(e)
    return t * t * (3 - 2 * t)
  },
  Le = (e, t) => {
    let n = Y(t)
    switch (e) {
      case `linear`:
        return n
      case `ease-out`:
        return 1 - (1 - n) ** 3
      case `ease-in-out`:
        return X(n)
      default:
        return a(e)
    }
  },
  Re = (e) => {
    let t = Y(e)
    return 1 + 2.70158 * (t - 1) ** 3 + 1.70158 * (t - 1) ** 2
  },
  ze = { wave: 17, dissolve: 29, sparkle: 31, reroll: 53, shimmerTiming: 59 },
  Be = 101,
  Ve = 0.3,
  He = 1.4,
  Ue = 0.02,
  We = 0.004,
  Ge = 0.999,
  Ke = 20,
  Z = 0.5,
  qe = [
    `a[href]`,
    `button`,
    `input`,
    `select`,
    `textarea`,
    `label`,
    `summary`,
    `[role='button']`,
    `[role='link']`,
    `[role='menu']`,
    `[role='menuitem']`,
    `[role='dialog']`,
    `[contenteditable='true']`,
    `[tabindex]:not([tabindex='-1'])`,
  ].join(`, `),
  Je = (e, t) =>
    e === t ||
    (e !== null &&
      t !== null &&
      Math.abs(e.left - t.left) < Z &&
      Math.abs(e.top - t.top) < Z &&
      Math.abs(e.right - t.right) < Z &&
      Math.abs(e.bottom - t.bottom) < Z),
  Ye = (e, t) => e.length === t.length && e.every((e, n) => Je(e, t[n])),
  Xe = ({
    container: e,
    canvas: t,
    getElements: n,
    layout: r,
    waiting: i,
    motion: o,
  }) => {
    let s = t.getContext(`2d`)
    if (!s) return null
    let c = r,
      l = !1,
      u = !1,
      d = ``,
      f = null,
      p = [],
      m = [],
      h = null,
      g = new Uint8Array(),
      _ = new Uint8Array(),
      v = new Float32Array(),
      y = 1,
      b = new Float32Array(),
      x = new Uint32Array(),
      ee = 0,
      S = new Uint8Array(),
      C = new Uint8Array(),
      w = new Float64Array(),
      T = 0,
      te = new Uint8Array(),
      ne = new Uint8Array(),
      re = 0,
      ie = 0,
      E = 0,
      ae = !1,
      D = 0,
      O = null,
      k = new Float32Array(),
      j = new Float32Array(),
      M = new Float32Array(),
      N = new Float64Array(),
      P = new Float32Array(),
      F = [],
      I = null,
      L = null,
      R = 0,
      z = 0,
      B,
      oe,
      se = 0,
      ce = new Set(),
      le = (e, t) => {
        let n = window.setTimeout(() => {
          ;(ce.delete(n), e())
        }, t)
        ce.add(n)
      },
      ue = () =>
        getComputedStyle(e).getPropertyValue(`--border-primary`).trim(),
      de = () => (f ? [f, ...p] : p),
      V = new ResizeObserver(() => W())
    V.observe(e)
    let fe = (e) => {
        for (let t of m) e.includes(t) || V.unobserve(t)
        for (let t of e) m.includes(t) || V.observe(t)
        m = [...e]
      },
      pe = () => {
        let { content: t, protected: r } = n(),
          i = r.filter((e, n) => e !== t && r.indexOf(e) === n)
        fe(t ? [t, ...i] : i)
        let a = e.getBoundingClientRect(),
          o = (e) => {
            let t = e.getBoundingClientRect()
            return t.width > 0 && t.height > 0
              ? {
                  left: t.left - a.left,
                  top: t.top - a.top,
                  right: t.right - a.left,
                  bottom: t.bottom - a.top,
                }
              : null
          },
          s = t ? o(t) : null,
          c = i.flatMap((e) => {
            let t = o(e)
            return t ? [t] : []
          }),
          l = !Je(f, s) || !Ye(p, c)
        return ((f = s), (p = c), l)
      },
      me = () => {
        if (!h) return
        let e = de()
        g = Ee(h, f ? [f] : [], A.protectedSpacePx, 0)
        for (let e of p) {
          let t = Ae(h, e, A.protectedSpacePx, A.protectedTopRadiusPx)
          for (let e = 0; e < t.length; e++) g[e] |= t[e]
        }
        ;((_ = Ee(
          h,
          f ? [f] : [],
          A.clearSpace.paddingPx,
          A.clearSpace.radiusPx,
        )),
          (v = je(h, e)),
          (y = 1))
        for (let e of v) y = Math.max(y, e)
        b = Me(h)
      },
      he = () =>
        h
          ? Ne({
              lattice: h,
              design: A,
              coverage: c.coverage,
              diagonal: c.diagonal,
              seed: A.seed,
              contentRects: de(),
              protectedMask: g,
              ditherSeedAt: (e) => A.seed + x[e] * Be,
              shift: ie,
              grow: E,
            })
          : new Uint8Array(),
      ge = (e, t) => (w[e] <= t ? C[e] : S[e]),
      _e = (e, t, n) => {
        if (e === `instant` || t <= 0) return () => 0
        re += 1
        let r = re,
          { waveJitter: i } = J,
          o = (e, a) => Le(n, a) * t * (1 - i) + U(e, r, ze.wave) * t * i
        switch (e) {
          case `dissolve`:
            return (e) => U(e, r, ze.dissolve) * t
          case `fromCorners`:
            return (e) => o(e, b[e])
          case `fromContent`:
            return (e) => o(e, v[e] / y)
          case `towardContent`:
            return (e) => o(e, 1 - v[e] / y)
          default:
            return a(e)
        }
      },
      H = (e, t, n = `linear`) => {
        if (!h) return
        let r = he(),
          i = performance.now(),
          a = o ? _e(e, t, n) : () => 0
        for (let e = 0; e < r.length; e++)
          r[e] !== C[e] &&
            ((S[e] = ge(e, i)),
            (C[e] = r[e]),
            (w[e] = i + (g[e] ? 0 : a(e))),
            (T = Math.max(T, w[e])))
        ;((te = ke(r, h)), $())
      },
      ye = (e) => {
        h = e
        let t = e.columns * e.rows
        ;((x = new Uint32Array(t)), me())
        let n = he()
        ;((S = Uint8Array.from(n)),
          (C = n),
          (w = new Float64Array(t)),
          (T = 0),
          (te = ke(n, e)),
          (ne = new Uint8Array(t)),
          (k = new Float32Array(t)),
          (j = new Float32Array(t)),
          (M = new Float32Array(t)),
          (N = new Float64Array(t)),
          (P = new Float32Array(t)),
          (F = []))
      },
      be = () => {
        ;(S.fill(0), C.fill(0), w.fill(0), H(`fromCorners`, J.waveMs))
      },
      W = () => {
        if (l) return
        let t = e.clientWidth,
          n = e.clientHeight
        if (t === 0 || n === 0) return
        let r = pe(),
          i = ve(t, n, A.cellPx)
        if (!h || i.columns !== h.columns || i.rows !== h.rows) {
          ;(ye(i), u || ((u = !0), o && be()), $())
          return
        }
        let a = i.width !== h.width || i.height !== h.height
        ;((h = i),
          (r || a) && (me(), H(`fromContent`, J.waveMs * J.contentWaveShare)))
      },
      G = () => {
        z === 0 &&
          !l &&
          (z = window.requestAnimationFrame(() => {
            ;((z = 0), W())
          }))
      },
      Se = (e) => {
        ee += 1
        for (let t = 0; t < x.length; t++)
          U(t, ee, ze.reroll) < e && (x[t] = ee)
      },
      Ce = () => {
        ;(window.clearTimeout(oe), (oe = void 0))
      },
      we = () => {
        if ((Ce(), !o || l || document.hidden)) return
        let { stepMs: e, jitter: t, intervalMs: n, amount: r } = J.shimmer
        se += 1
        let i = e * (1 + (U(se, A.seed, ze.shimmerTiming) * 2 - 1) * t)
        oe = window.setTimeout(() => {
          ;((oe = void 0), Se(Math.min(1, (r * i) / n)), H(`dissolve`, i), we())
        }, i)
      },
      { breathe: K } = J,
      Te = () => {
        D += 1
        let e = D,
          t = () => {
            e === D &&
              ((E = K.depthCells),
              H(`towardContent`, K.inMs, K.easing),
              le(() => {
                e === D &&
                  ((E = 0),
                  H(`fromContent`, K.outMs, K.easing),
                  le(t, K.outMs + K.holdMs))
              }, K.inMs + K.holdMs))
          }
        t()
      },
      De = () => {
        ;((D += 1), E > 0 && ((E = 0), H(`fromContent`, K.outMs, K.easing)))
      },
      Oe = () => {
        let { durationMs: e, settleAt: t, settleWaveShare: n } = J.sparkle
        ;((I = performance.now()),
          $(),
          le(() => {
            ;(Se(1), H(`dissolve`, J.waveMs * n))
          }, e * t))
      },
      Pe = () => {
        J.shake.stepsCells.forEach((e, t) => {
          le(() => {
            ;((ie = e), H(`instant`, 0))
          }, t * J.shake.stepMs)
        })
      },
      q = () => {
        ;((L = performance.now()), $())
      },
      Fe = (e, t) => {
        if (L === null) return 0
        let { spreadMs: n, riseMs: r, holdMs: i, fallMs: a } = J.fillIn,
          o = t - L - (v[e] / y) * n
        return o <= 0
          ? 0
          : o < r
            ? X(o / r)
            : o < r + i
              ? 1
              : Math.max(0, 1 - X((o - r - i) / a))
      },
      { extrude: Y, press: Z } = J,
      Xe = (Z.rippleReachCells + Z.rippleWidthCells) / Z.rippleCellsPerMs,
      Ze = (e) =>
        e.releasedAt === null
          ? 1 / 0
          : Math.max(e.releasedAt, e.start + Z.downMs + Z.holdMs),
      Qe = (e) => Ze(e) + Math.max(Z.upMs, Xe),
      $e = (e, t) => {
        let n = 1
        for (let r of F) {
          if (r.index !== e) continue
          let i = t - r.start,
            a = t - Ze(r)
          i < Z.downMs
            ? (n = Math.min(n, 1 - X(i / Z.downMs)))
            : a < 0
              ? (n = 0)
              : a < Z.upMs && (n = Math.min(n, Re(a / Z.upMs)))
        }
        return n
      },
      et = (e, t, n) => {
        let r = 0,
          i = e % n,
          a = Math.floor(e / n)
        for (let e of F) {
          let n = t - Ze(e),
            o = Math.hypot(i - e.column, a - e.row)
          if (n <= 0 || o === 0 || o > Z.rippleReachCells) continue
          let s = Math.max(
            0,
            1 - Math.abs(o - n * Z.rippleCellsPerMs) / Z.rippleWidthCells,
          )
          r += X(s) * Z.rippleLift * (1 - o / (Z.rippleReachCells + 1))
        }
        return r
      },
      tt = (e) => {
        if (!O || !h) return 0
        let { columns: t, cellWidth: n, cellHeight: r } = h
        return X(
          1 -
            Math.hypot(
              (e % t) + 0.5 - O.x / n,
              Math.floor(e / t) + 0.5 - O.y / r,
            ) /
              (Y.radiusCells + 0.5),
        )
      },
      nt = (e, t) => {
        let n = P[e]
        return n <= 0
          ? M[e]
          : j[e] + (M[e] - j[e]) * Le(Y.easing, (t - N[e]) / n)
      },
      rt = (e) => {
        if (!(e instanceof Element)) return !1
        if (e.closest(qe)) return !0
        let { content: t, protected: r } = n()
        return t?.contains(e) === !0 || r.some((t) => t.contains(e))
      },
      it = (t) => {
        let n = e.getBoundingClientRect()
        return { x: t.clientX - n.left, y: t.clientY - n.top }
      },
      at = (e) => {
        if (e.pointerType === `touch`) return
        let t = rt(e.target) ? null : it(e)
        ;(t !== null || O !== null) && ((O = t), $())
      },
      ot = (e) => {
        if (!h || e.button !== 0 || e.pointerType === `touch` || rt(e.target))
          return
        let { x: t, y: n } = it(e),
          r = xe(h, t, n),
          i = performance.now()
        !r ||
          !ge(r.index, i) ||
          k[r.index] < Ve ||
          (F.push({ ...r, start: i, releasedAt: null }), $())
      },
      Q = () => {
        let e = performance.now()
        for (let t of F) t.releasedAt ??= e
        $()
      },
      st = () => {
        ;((O = null), Q())
      },
      ct = (e) => {
        let t = !1
        if (o)
          for (let n = 0; n < k.length; n++) {
            let r = tt(n)
            if (Math.abs(r - M[n]) > 0.01) {
              let t = nt(n, e)
              ;((j[n] = t),
                (M[n] = r),
                (N[n] = e),
                (P[n] = r > t ? Y.riseMs : Y.fallMs))
            }
            e < N[n] + P[n] ? ((k[n] = nt(n, e)), (t = !0)) : (k[n] = M[n])
          }
        ;((F = F.filter((t) => e < Qe(t))),
          I !== null && e >= I + J.sparkle.durationMs && (I = null))
        let { spreadMs: n, riseMs: r, holdMs: i, fallMs: a } = J.fillIn
        return (
          L !== null && e >= L + n + r + i + a && (L = null),
          t || F.length > 0 || I !== null || L !== null
        )
      },
      lt = (e) => {
        if (!h) return
        let { columns: n, rows: r } = h,
          i = n * r,
          a =
            I === null
              ? 0
              : J.sparkle.chance * (1 - (e - I) / J.sparkle.durationMs),
          c = Math.floor(e / J.sparkle.frameMs),
          l = [],
          u = []
        for (let t = 0; t < i; t++) {
          let n = ge(t, e)
          if (!g[t]) {
            a > 0 && te[t] && U(t, c, ze.sparkle) < a && (n = +!n)
            let r = _[t] ? 0 : Fe(t, e)
            r > We &&
              (l.push({ index: t, amount: r }),
              r >= Ge ? (n = 1) : n || u.push({ index: t, amount: r }))
          }
          ne[t] = n
        }
        let f = []
        if (o)
          for (let t = 0; t < i; t++) {
            if (!ne[t]) continue
            let r = Math.min(
              He,
              Math.max(0, k[t] * $e(t, e) + (F.length > 0 ? et(t, e, n) : 0)),
            )
            r >= Ue && f.push({ index: t, amount: r })
          }
        Ie(s, t, {
          lattice: h,
          lineColor: d,
          pixelRatio: window.devicePixelRatio || 1,
          shown: ne,
          washed: l,
          rising: u,
          lifted: f,
        })
      },
      ut = (e) => {
        if (e >= T) return 1 / 0
        let t = 1 / 0
        for (let n = 0; n < w.length; n++) w[n] > e && w[n] < t && (t = w[n])
        return t
      },
      dt = (e) => {
        if (((R = 0), l)) return
        let t = ct(e)
        if ((lt(e), t)) {
          $()
          return
        }
        let n = ut(e)
        n - e <= Ke
          ? $()
          : Number.isFinite(n) &&
            (B = window.setTimeout($, n - performance.now()))
      },
      $ = () => {
        ;(window.clearTimeout(B),
          (B = void 0),
          !(R !== 0 || l || document.hidden) &&
            (R = window.requestAnimationFrame(dt)))
      },
      ft = () => {
        document.hidden
          ? (window.cancelAnimationFrame(R),
            (R = 0),
            window.clearTimeout(B),
            Ce())
          : ((d = ue()), W(), we(), $())
      },
      pt = new MutationObserver(() => {
        let e = ue()
        e !== d && ((d = e), $())
      }),
      mt = { attributes: !0, attributeFilter: [`class`, `style`, `data-theme`] }
    ;(pt.observe(document.documentElement, mt), pt.observe(document.body, mt))
    let ht,
      gt = () => {
        ;(ht?.removeEventListener(`change`, _t),
          (ht = window.matchMedia?.(
            `(resolution: ${window.devicePixelRatio || 1}dppx)`,
          )),
          ht?.addEventListener(`change`, _t))
      },
      _t = () => {
        ;(gt(), $())
      },
      vt = new MutationObserver(G)
    ;(e.parentElement &&
      vt.observe(e.parentElement, { childList: !0, subtree: !0 }),
      window.addEventListener(`resize`, G),
      document.addEventListener(`scroll`, G, { capture: !0, passive: !0 }),
      document.addEventListener(`visibilitychange`, ft),
      o &&
        (window.addEventListener(`pointermove`, at, { passive: !0 }),
        window.addEventListener(`pointerdown`, ot),
        window.addEventListener(`pointerup`, Q),
        window.addEventListener(`pointercancel`, Q),
        window.addEventListener(`blur`, st),
        document.documentElement.addEventListener(`mouseleave`, st)))
    let yt = (e) => {
      e !== ae && ((ae = e), o && (ae ? Te() : De()))
    }
    return (
      (d = ue()),
      gt(),
      W(),
      we(),
      yt(i),
      {
        setLayout: (e) => {
          ;(e.coverage !== c.coverage || e.diagonal !== c.diagonal) &&
            ((c = e), H(`dissolve`, J.waveMs))
        },
        setWaiting: yt,
        react: (e) => {
          if (o && h)
            switch (e) {
              case `press`:
                Oe()
                return
              case `error`:
                Pe()
                return
              case `success`:
              case `fillIn`:
                q()
                return
              default:
                a(e)
            }
        },
        refresh: W,
        destroy: () => {
          ;((l = !0),
            window.cancelAnimationFrame(R),
            window.cancelAnimationFrame(z),
            window.clearTimeout(B),
            Ce())
          for (let e of ce) window.clearTimeout(e)
          ;(ce.clear(),
            V.disconnect(),
            pt.disconnect(),
            vt.disconnect(),
            ht?.removeEventListener(`change`, _t),
            window.removeEventListener(`resize`, G),
            document.removeEventListener(`scroll`, G, { capture: !0 }),
            document.removeEventListener(`visibilitychange`, ft),
            window.removeEventListener(`pointermove`, at),
            window.removeEventListener(`pointerdown`, ot),
            window.removeEventListener(`pointerup`, Q),
            window.removeEventListener(`pointercancel`, Q),
            window.removeEventListener(`blur`, st),
            document.documentElement.removeEventListener(`mouseleave`, st))
        },
      }
    )
  }

export function createRenderGrid({
  container,
  canvas,
  content = null,
  protectedElements = [],
  coverage = 'corners',
  diagonal = 'secondary',
  motion = !window.matchMedia('(prefers-reduced-motion: reduce)').matches,
}) {
  if (!container || !canvas)
    throw new TypeError('container and canvas are required')
  return Xe({
    container,
    canvas,
    getElements: () => ({ content, protected: protectedElements }),
    layout: { coverage, diagonal },
    waiting: false,
    motion,
  })
}
