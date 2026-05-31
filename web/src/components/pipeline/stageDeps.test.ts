import { describe, it, expect } from 'vitest'
import type { PipelineStage } from '../../api/pipeline'
import { dependsOn, canAddNeed, eligibleNeeds, toggleNeed, hasAnyNeeds } from './stageDeps'

function stage(id: string, needs: string[] = []): PipelineStage {
  return { id, name: id.toUpperCase(), kind: 'custom', needs, jobs: [] }
}

describe('stageDeps', () => {
  // src → build → deploy (deploy needs build, build needs src)
  const chain: PipelineStage[] = [stage('src'), stage('build', ['src']), stage('deploy', ['build'])]

  describe('dependsOn', () => {
    it('finds direct and transitive deps', () => {
      expect(dependsOn(chain, 'deploy', 'build')).toBe(true)
      expect(dependsOn(chain, 'deploy', 'src')).toBe(true) // transitive
      expect(dependsOn(chain, 'build', 'src')).toBe(true)
    })
    it('is directional', () => {
      expect(dependsOn(chain, 'src', 'deploy')).toBe(false)
      expect(dependsOn(chain, 'build', 'deploy')).toBe(false)
    })
    it('handles missing/empty', () => {
      expect(dependsOn(chain, 'src', 'src')).toBe(false)
      expect(dependsOn([], 'a', 'b')).toBe(false)
    })
  })

  describe('canAddNeed', () => {
    it('rejects self', () => {
      expect(canAddNeed(chain, 'build', 'build')).toBe(false)
    })
    it('rejects cycle-closing edge', () => {
      // deploy depends on build; making build need deploy would cycle.
      expect(canAddNeed(chain, 'build', 'deploy')).toBe(false)
      expect(canAddNeed(chain, 'src', 'deploy')).toBe(false)
    })
    it('allows safe edges', () => {
      // build can additionally need... nothing new here; src can need build is unsafe(build needs src).
      expect(canAddNeed(chain, 'deploy', 'src')).toBe(true) // already implied, still safe
      const flat = [stage('a'), stage('b'), stage('c')]
      expect(canAddNeed(flat, 'c', 'a')).toBe(true)
      expect(canAddNeed(flat, 'c', 'b')).toBe(true)
    })
  })

  describe('eligibleNeeds', () => {
    it('excludes self and cycle-creating stages', () => {
      // For 'build': self excluded; 'deploy' excluded (depends on build); 'src' eligible.
      const ids = eligibleNeeds(chain, 'build').map((s) => s.id)
      expect(ids).toEqual(['src'])
    })
    it('flat graph: all others eligible', () => {
      const flat = [stage('a'), stage('b'), stage('c')]
      expect(eligibleNeeds(flat, 'b').map((s) => s.id)).toEqual(['a', 'c'])
    })
  })

  describe('toggleNeed', () => {
    it('adds and removes immutably', () => {
      expect(toggleNeed([], 'x')).toEqual(['x'])
      expect(toggleNeed(['x', 'y'], 'x')).toEqual(['y'])
      const orig = ['a']
      toggleNeed(orig, 'b')
      expect(orig).toEqual(['a']) // unchanged
    })
  })

  describe('hasAnyNeeds', () => {
    it('detects explicit needs vs linear', () => {
      expect(hasAnyNeeds(chain)).toBe(true)
      expect(hasAnyNeeds([stage('a'), stage('b')])).toBe(false)
    })
  })
})
