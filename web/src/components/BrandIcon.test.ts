import faviconSource from '../../public/favicon.svg?raw'
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import BrandIcon from './BrandIcon.vue'

function artwork(svg: Element) {
  const ids = Array.from(svg.querySelectorAll('[id]'), (element) => element.id)
  const normalize = (value: string) => {
    const index = ids.indexOf(value)
    if (index !== -1) return 'gradient-' + index
    return value.replace(
      /url\(#([^)]*)\)/g,
      (_, id: string) => 'url(#gradient-' + ids.indexOf(id) + ')',
    )
  }
  return Array.from(svg.querySelectorAll('*'), (element) => ({
    tag: element.localName,
    attributes: Array.from(element.attributes)
      .filter(({ name }) => !name.startsWith('data-v-'))
      .map(({ name, value }) => [name, normalize(value)])
      .sort(([left], [right]) => left.localeCompare(right)),
  }))
}

describe('shared BrandIcon', () => {
  it('preserves the favicon artwork and square viewBox', () => {
    const favicon = new DOMParser().parseFromString(
      faviconSource,
      'image/svg+xml',
    ).documentElement
    const wrapper = mount(BrandIcon)
    try {
      expect(wrapper.attributes('viewBox')).toBe(
        favicon.getAttribute('viewBox'),
      )
      expect(wrapper.attributes('preserveAspectRatio')).toBe('xMidYMid meet')
      expect(artwork(wrapper.element)).toEqual(artwork(favicon))
    } finally {
      wrapper.unmount()
    }
  })

  it('keeps gradient references unique when reused on the same page', () => {
    const wrapper = mount({
      components: { BrandIcon },
      template: '<div><BrandIcon /><BrandIcon /></div>',
    })
    try {
      const ids = wrapper
        .findAll('linearGradient')
        .map((gradient) => gradient.attributes('id'))
      expect(ids).toHaveLength(4)
      expect(new Set(ids).size).toBe(4)
      for (const icon of wrapper.findAll('svg')) {
        const localIds = icon
          .findAll('linearGradient')
          .map((gradient) => gradient.attributes('id'))
        expect(
          icon.findAll('rect').map((rect) => rect.attributes('fill')),
        ).toEqual(localIds.map((id) => 'url(#' + id + ')'))
      }
    } finally {
      wrapper.unmount()
    }
  })

  it('accepts caller sizing and classes while remaining decorative', () => {
    const wrapper = mount(BrandIcon, {
      attrs: { class: 'shell-brand', width: 30, height: 30 },
    })
    try {
      expect(wrapper.classes()).toEqual(
        expect.arrayContaining(['brand-icon', 'shell-brand']),
      )
      expect(wrapper.attributes('width')).toBe('30')
      expect(wrapper.attributes('height')).toBe('30')
      expect(wrapper.attributes('aria-hidden')).toBe('true')
      expect(wrapper.attributes('focusable')).toBe('false')
    } finally {
      wrapper.unmount()
    }
  })
})
