import { useEffect, useRef, useState, type RefObject } from 'react'
import { easeSpeed, flowOpacity } from './pipelineActivity'
import type { PathId } from './pipelinePaths'

// A frame after a long pause, such as a background tab, moves no further than this.
const MAX_FRAME_MS = 100

interface Motion {
  speed: number
  position: number
}

// One animation frame loop moves the dots of every path in the element. It runs
// only while the element is on screen and something moves, and keeps each
// path's speed and position between polls so the dots never jump.
export function useFlowAnimation(root: RefObject<HTMLElement | null>, targets: Record<PathId, number> | null) {
  const onScreen = useOnScreen(root)
  const motions = useRef(new Map<PathId, Motion>())

  useEffect(() => {
    const element = root.current
    if (!targets) motions.current.clear()
    if (!element || !targets || !onScreen) return
    let frame = 0
    let last: number | null = null
    const step = (now: number) => {
      const elapsed = last === null ? 0 : Math.min(now - last, MAX_FRAME_MS)
      last = now
      const moving = advance(motions.current, targets, elapsed)
      drawFlows(element, motions.current)
      frame = moving ? requestAnimationFrame(step) : 0
    }
    frame = requestAnimationFrame(step)
    return () => cancelAnimationFrame(frame)
  }, [root, targets, onScreen])
}

function advance(motions: Map<PathId, Motion>, targets: Record<PathId, number>, elapsedMs: number): boolean {
  let moving = false
  for (const id of Object.keys(targets) as PathId[]) {
    const motion = motions.get(id) ?? { speed: 0, position: 0 }
    motion.speed = easeSpeed(motion.speed, targets[id], elapsedMs)
    motion.position += (motion.speed * elapsedMs) / 1000
    motions.set(id, motion)
    moving ||= motion.speed > 0 || targets[id] > 0
  }
  return moving
}

// A negative offset moves the dashes from the start of a path towards its arrowhead.
function drawFlows(root: HTMLElement, motions: Map<PathId, Motion>) {
  for (const dashes of root.querySelectorAll<SVGPathElement>('.pipeline-flow-dash')) {
    const id = dashes.closest<SVGGElement>('[data-path]')?.dataset.path as PathId | undefined
    const motion = id && motions.get(id)
    if (!motion) continue
    dashes.style.strokeDashoffset = String(-motion.position)
    dashes.style.opacity = String(flowOpacity(motion.speed))
  }
}

function useOnScreen(root: RefObject<HTMLElement | null>): boolean {
  const [onScreen, setOnScreen] = useState(true)
  useEffect(() => {
    const element = root.current
    if (!element) return
    const observer = new IntersectionObserver(([entry]) => setOnScreen(entry.isIntersecting))
    observer.observe(element)
    return () => observer.disconnect()
  }, [root])
  return onScreen
}
