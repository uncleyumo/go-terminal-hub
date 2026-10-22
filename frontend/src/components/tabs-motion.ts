// 会话列表 ⇄ 顶栏标签之间那一段「飞过去」。
//
// 只做视觉：两端坐标都是从真实元素上量的，飞完就把临时元素删掉，不碰任何状态。
// 状态怎么切、什么时候切，由调用方（App.vue）安排 —— 这里只管「从 A 飞到 B」。
//
// 为什么用 position:fixed + getBoundingClientRect：两端在不同的滚动容器里
// （列表会滚、标签条会横滚），只有视口坐标是两边都认的。

export interface Chip {
  id: string
  label: string
  running: boolean
}

/** 两端各自挂在哪个属性上，见 App.vue 里 data-session-row / data-session-tab */
export type ChipSurface = 'row' | 'tab'

const ATTR: Record<ChipSurface, string> = {
  row: 'data-session-row',
  tab: 'data-session-tab',
}

function prefersReducedMotion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

export function measureChips(
  where: ChipSurface,
  chips: Chip[],
): Record<string, DOMRect> {
  const out: Record<string, DOMRect> = {}
  for (const chip of chips) {
    const el = document.querySelector<HTMLElement>(`[${ATTR[where]}="${chip.id}"]`)
    if (el) out[chip.id] = el.getBoundingClientRect()
  }
  return out
}

/**
 * 让每个 chip 从 source 的矩形飞到 target 的矩形。
 *
 * 逐条错开 45ms：一起飞会糊成一团，错开之后眼睛能跟着其中一条走完整个行程。
 * 飞到终点时淡出 —— 目标位置上那枚真标签紧接着亮起来（见 revealTabs）。
 */
export function flyChips(
  chips: Chip[],
  source: Record<string, DOMRect>,
  target: Record<string, DOMRect>,
  stagger = 45,
) {
  if (prefersReducedMotion()) return

  chips.forEach((chip, i) => {
    const a = source[chip.id]
    const b = target[chip.id]
    if (!a || !b || a.width === 0) return

    const ghost = document.createElement('div')
    // 名字单独一个 span：flex 容器里直接放文字，text-overflow 不生效
    const label = document.createElement('span')
    label.textContent = chip.label
    label.style.cssText = 'min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap'
    ghost.appendChild(label)
    ghost.style.cssText = [
      'position:fixed',
      `left:${a.left}px`,
      `top:${a.top}px`,
      `width:${a.width}px`,
      `height:${a.height}px`,
      'display:flex',
      'align-items:center',
      'padding:0 10px',
      'border-radius:8px',
      'background:var(--color-overlay)',
      'border:1px solid var(--color-line-strong)',
      'color:var(--color-ink)',
      'font-size:12.5px',
      'line-height:1',
      'white-space:nowrap',
      'overflow:hidden',
      'text-overflow:ellipsis',
      'pointer-events:none',
      'z-index:9000',
      'transform-origin:0 0',
      'box-shadow:0 10px 28px rgba(0,0,0,.45)',
    ].join(';')
    if (!chip.running) ghost.style.color = 'var(--color-ink-dim)'
    document.body.appendChild(ghost)

    const dx = b.left - a.left
    const dy = b.top - a.top
    const sx = b.width / a.width
    const sy = b.height / a.height

    ghost
      .animate(
        [
          { transform: 'translate(0,0) scale(1,1)', opacity: 1 },
          {
            transform: `translate(${dx * 0.55}px,${dy * 0.55}px) scale(${1 + (sx - 1) * 0.4},${1 + (sy - 1) * 0.4})`,
            opacity: 1,
            offset: 0.55,
          },
          { transform: `translate(${dx}px,${dy}px) scale(${sx},${sy})`, opacity: 0 },
        ],
        {
          duration: 460,
          delay: i * stagger,
          easing: 'cubic-bezier(.22,.8,.28,1)',
          fill: 'forwards',
        },
      )
      .finished.catch(() => undefined)
      .finally(() => ghost.remove())
  })
}

/** 标签落地之后才亮：飞行途中它一直是透明的，别让同一件事出现两个。 */
export function revealTabs(chips: Chip[], stagger = 45, baseDelay = 260) {
  if (prefersReducedMotion()) return

  chips.forEach((chip, i) => {
    const el = document.querySelector<HTMLElement>(`[data-session-tab="${chip.id}"]`)
    if (!el) return
    el.animate([{ opacity: 0 }, { opacity: 1 }], {
      duration: 140,
      delay: baseDelay + i * stagger,
      easing: 'ease-out',
      fill: 'backwards',
    })
  })
}
