const showcase = document.querySelector('.showcase');
const controls = showcase.querySelector('.showcase-controls');
const rotation = controls.querySelector('.showcase-rotation');
const tabs = [...controls.querySelectorAll('[role="tab"]')];
const panels = tabs.map(tab => document.getElementById(tab.getAttribute('aria-controls')));

const rotationDelay = 6000;
const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
let selectedIndex = 0;
let playbackOverride;
let inView = false;
let hover = false;
let timer;

function isPaused() {
  return playbackOverride ?? reducedMotion.matches;
}

function updateRotation() {
  window.clearTimeout(timer);
  const paused = isPaused();
  const label = paused ? '播放自动轮播' : '暂停自动轮播';
  rotation.setAttribute('aria-label', label);
  rotation.title = label;
  rotation.toggleAttribute('data-paused', paused);
  const focused = document.activeElement;
  if (paused || !inView || hover || document.hidden ||
      (focused !== rotation && showcase.contains(focused))) return;
  timer = window.setTimeout(() => show((selectedIndex + 1) % tabs.length, false), rotationDelay);
}

function show(index, moveFocus) {
  selectedIndex = index;
  tabs.forEach((tab, position) => {
    const selected = position === index;
    tab.setAttribute('aria-selected', String(selected));
    tab.tabIndex = selected ? 0 : -1;
    panels[position].hidden = !selected;
  });
  if (moveFocus) tabs[index].focus();
  updateRotation();
}

tabs.forEach((tab, index) => {
  const panel = panels[index];
  panel.setAttribute('role', 'tabpanel');
  panel.setAttribute('aria-labelledby', tab.id);
  panel.tabIndex = 0;
  tab.addEventListener('click', () => show(index, false));
  tab.addEventListener('keydown', event => {
    let next;
    switch (event.key) {
      case 'ArrowRight': next = (index + 1) % tabs.length; break;
      case 'ArrowLeft': next = (index + tabs.length - 1) % tabs.length; break;
      case 'Home': next = 0; break;
      case 'End': next = tabs.length - 1; break;
      default: return;
    }
    event.preventDefault();
    show(next, true);
  });
});
rotation.addEventListener('click', () => {
  playbackOverride = !isPaused();
  updateRotation();
});
showcase.addEventListener('pointerenter', event => {
  if (event.pointerType === 'touch') return;
  hover = true;
  updateRotation();
});
showcase.addEventListener('pointerleave', () => {
  hover = false;
  updateRotation();
});
showcase.addEventListener('focusin', updateRotation);
showcase.addEventListener('focusout', () => window.queueMicrotask(updateRotation));
document.addEventListener('visibilitychange', updateRotation);
reducedMotion.addEventListener('change', updateRotation);
const observer = new IntersectionObserver(([entry]) => {
  inView = entry.intersectionRatio >= 0.15;
  updateRotation();
}, {threshold: 0.15});
observer.observe(showcase.querySelector('.showcase-shots'));
updateRotation();
controls.hidden = false;
