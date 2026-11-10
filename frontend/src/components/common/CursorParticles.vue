<template>
  <canvas ref="canvas" class="cursor-particles" aria-hidden="true"></canvas>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

type Particle = {
  x: number
  y: number
  vx: number
  vy: number
  size: number
  life: number
  maxLife: number
  color: string
}

const MAX_PARTICLES = 180
const PARTICLE_COLORS = ['#39d7ed', '#7c8cff', '#b59bff', '#8af4ff']
const canvas = ref<HTMLCanvasElement | null>(null)

let context: CanvasRenderingContext2D | null = null
let particles: Particle[] = []
let previousX: number | null = null
let previousY: number | null = null
let frameId = 0
let lastFrameTime = 0

function resizeCanvas() {
  const element = canvas.value
  context = element?.getContext('2d') ?? null
  if (!element || !context) return

  const scale = Math.min(window.devicePixelRatio || 1, 2)
  element.width = Math.round(window.innerWidth * scale)
  element.height = Math.round(window.innerHeight * scale)
  context.setTransform(scale, 0, 0, scale, 0, 0)
}

function addParticle(x: number, y: number, directionX: number, directionY: number) {
  if (particles.length >= MAX_PARTICLES) particles.shift()

  const sideX = -directionY
  const sideY = directionX
  const spread = (Math.random() - 0.5) * 34
  const drift = 10 + Math.random() * 25
  const life = 0.32 + Math.random() * 0.38

  particles.push({
    x: x + (Math.random() - 0.5) * 3,
    y: y + (Math.random() - 0.5) * 3,
    vx: -directionX * drift + sideX * spread,
    vy: -directionY * drift + sideY * spread,
    size: 0.9 + Math.random() * 2,
    life,
    maxLife: life,
    color: PARTICLE_COLORS[Math.floor(Math.random() * PARTICLE_COLORS.length)],
  })
}

function animate(now: number) {
  if (!context) {
    frameId = 0
    return
  }

  const delta = Math.min((now - lastFrameTime) / 1000, 0.032)
  lastFrameTime = now
  context.clearRect(0, 0, window.innerWidth, window.innerHeight)

  for (let index = particles.length - 1; index >= 0; index -= 1) {
    const particle = particles[index]
    particle.life -= delta
    if (particle.life <= 0) {
      particles.splice(index, 1)
      continue
    }

    particle.x += particle.vx * delta
    particle.y += particle.vy * delta
    particle.vx *= 0.985
    particle.vy = particle.vy * 0.985 + 8 * delta

    context.globalAlpha = Math.pow(particle.life / particle.maxLife, 1.4)
    context.fillStyle = particle.color
    context.beginPath()
    context.arc(particle.x, particle.y, particle.size, 0, Math.PI * 2)
    context.fill()
  }

  context.globalAlpha = 1
  if (particles.length > 0) {
    frameId = requestAnimationFrame(animate)
  } else {
    context.clearRect(0, 0, window.innerWidth, window.innerHeight)
    frameId = 0
  }
}

function onPointerMove(event: PointerEvent) {
  if (event.pointerType !== 'mouse') return

  const x = event.clientX
  const y = event.clientY
  if (previousX !== null && previousY !== null) {
    const deltaX = x - previousX
    const deltaY = y - previousY
    const distance = Math.hypot(deltaX, deltaY)

    if (distance > 1) {
      const directionX = deltaX / distance
      const directionY = deltaY / distance
      const steps = Math.min(24, Math.ceil(distance / 6))

      for (let step = 1; step <= steps; step += 1) {
        const progress = step / steps
        const particleX = previousX + deltaX * progress
        const particleY = previousY + deltaY * progress
        addParticle(particleX, particleY, directionX, directionY)
        if (Math.random() > 0.72) addParticle(particleX, particleY, directionX, directionY)
      }

      if (!frameId) {
        lastFrameTime = performance.now()
        frameId = requestAnimationFrame(animate)
      }
    }
  }

  previousX = x
  previousY = y
}

function onPointerLeave() {
  previousX = null
  previousY = null
}

function onVisibilityChange() {
  if (document.visibilityState !== 'hidden') return

  previousX = null
  previousY = null
  particles = []
  if (frameId) cancelAnimationFrame(frameId)
  frameId = 0
  context?.clearRect(0, 0, window.innerWidth, window.innerHeight)
}

onMounted(() => {
  const supportsMouse = window.matchMedia('(hover: hover) and (pointer: fine)').matches
  const prefersReducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  if (!supportsMouse || prefersReducedMotion) return

  resizeCanvas()
  window.addEventListener('resize', resizeCanvas, { passive: true })
  window.addEventListener('pointermove', onPointerMove, { passive: true })
  window.addEventListener('blur', onPointerLeave)
  document.documentElement.addEventListener('pointerleave', onPointerLeave)
  document.addEventListener('visibilitychange', onVisibilityChange)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', resizeCanvas)
  window.removeEventListener('pointermove', onPointerMove)
  window.removeEventListener('blur', onPointerLeave)
  document.documentElement.removeEventListener('pointerleave', onPointerLeave)
  document.removeEventListener('visibilitychange', onVisibilityChange)
  if (frameId) cancelAnimationFrame(frameId)
})
</script>

<style scoped>
.cursor-particles {
  position: fixed;
  z-index: 40;
  inset: 0;
  width: 100vw;
  height: 100vh;
  pointer-events: none;
}

@media (prefers-reduced-motion: reduce) {
  .cursor-particles {
    display: none;
  }
}
</style>
