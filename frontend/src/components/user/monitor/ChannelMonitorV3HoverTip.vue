<template>
  <div
    class="v3-hover-anchor"
    @mouseenter="onEnter"
    @mouseleave="onLeave"
    @focusin="onEnter"
    @focusout="onLeave"
  >
    <slot />
    <Teleport to="body">
      <Transition name="v3-timeline-tooltip">
        <div
          v-if="open"
          class="v3-timeline-tooltip"
          role="tooltip"
          :style="tooltipStyle"
        >
          <slot name="tip" />
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const open = ref(false)
const tooltipStyle = ref<Record<string, string>>({})

function onEnter(event: Event) {
  const target = event.currentTarget
  if (!(target instanceof HTMLElement) || typeof window === 'undefined') return
  const rect = target.getBoundingClientRect()
  const viewportGutter = 16
  const maxTooltipWidth = Math.min(280, window.innerWidth - viewportGutter * 2)
  const center = rect.left + rect.width / 2
  let left = center
  let x = '-50%'
  if (center + maxTooltipWidth / 2 > window.innerWidth - viewportGutter) {
    left = window.innerWidth - viewportGutter
    x = '-100%'
  } else if (center - maxTooltipWidth / 2 < viewportGutter) {
    left = viewportGutter
    x = '0%'
  }
  tooltipStyle.value = {
    '--tooltip-left': `${left}px`,
    '--tooltip-top': `${rect.top - 8}px`,
    '--tooltip-x': x,
  }
  open.value = true
}

function onLeave() {
  open.value = false
}
</script>

<style scoped>
.v3-hover-anchor {
  min-width: 0;
  max-width: 100%;
}

.v3-timeline-tooltip {
  position: fixed;
  left: var(--tooltip-left, 50%);
  top: var(--tooltip-top, 0px);
  z-index: 50;
  width: max-content;
  max-width: min(280px, calc(100vw - 32px));
  transform: translateX(var(--tooltip-x, -50%)) translateY(-100%);
  border: 1px solid rgb(255 255 255 / 0.84);
  border-radius: 9px;
  background: rgb(15 23 42 / 0.92);
  padding: 6px 9px;
  color: rgb(248 250 252);
  font-size: 10px;
  font-weight: 600;
  line-height: 1.35;
  letter-spacing: 0;
  white-space: normal;
  box-shadow: 0 10px 24px rgb(15 23 42 / 0.2);
  pointer-events: none;
}

.v3-timeline-tooltip::after {
  position: absolute;
  left: 50%;
  bottom: -4px;
  width: 7px;
  height: 7px;
  transform: translateX(-50%) rotate(45deg);
  border-right: 1px solid rgb(255 255 255 / 0.84);
  border-bottom: 1px solid rgb(255 255 255 / 0.84);
  background: rgb(15 23 42 / 0.92);
  content: '';
}

.v3-timeline-tooltip-enter-active,
.v3-timeline-tooltip-leave-active {
  transition: opacity 100ms ease, transform 120ms cubic-bezier(0.22, 1, 0.36, 1);
}

.v3-timeline-tooltip-enter-from,
.v3-timeline-tooltip-leave-to {
  opacity: 0;
  transform: translateX(var(--tooltip-x, -50%)) translateY(calc(-100% + 3px)) scale(0.96);
}

@media (prefers-reduced-motion: reduce) {
  .v3-timeline-tooltip-enter-active,
  .v3-timeline-tooltip-leave-active {
    transition: none;
  }
}
</style>
