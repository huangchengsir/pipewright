import { describe, it, expect } from 'vitest'
import type { PipelineJob } from '../../api/pipeline'
import {
  jobDependsOn,
  canAddJobNeed,
  eligibleJobNeeds,
  hasAnyJobNeeds,
  layoutJobs,
} from './jobDeps'

function job(id: string, needs: string[] = []): PipelineJob {
  return { id, name: id.toUpperCase(), type: 'script', summary: '', config: {}, needs }
}

describe('jobDeps', () => {
  // A, B parallel roots; C needs both (serial after A and B)
  const fan: PipelineJob[] = [job('A'), job('B'), job('C', ['A', 'B'])]

  describe('jobDependsOn', () => {
    it('finds direct and transitive deps', () => {
      const chain = [job('A'), job('B', ['A']), job('C', ['B'])]
      expect(jobDependsOn(chain, 'C', 'B')).toBe(true)
      expect(jobDependsOn(chain, 'C', 'A')).toBe(true) // transitive
    })
    it('is directional', () => {
      expect(jobDependsOn(fan, 'A', 'C')).toBe(false)
      expect(jobDependsOn(fan, 'C', 'A')).toBe(true)
    })
  })

  describe('canAddJobNeed', () => {
    it('rejects self and cycle-closing edges', () => {
      expect(canAddJobNeed(fan, 'A', 'A')).toBe(false)
      // C depends on A; making A need C would cycle.
      expect(canAddJobNeed(fan, 'A', 'C')).toBe(false)
    })
    it('allows safe edges', () => {
      expect(canAddJobNeed(fan, 'C', 'A')).toBe(true)
      expect(canAddJobNeed(fan, 'B', 'A')).toBe(true)
    })
  })

  describe('eligibleJobNeeds', () => {
    it('excludes self and cycle-creating jobs', () => {
      const ids = eligibleJobNeeds(fan, 'A').map((j) => j.id)
      expect(ids).toContain('B')
      expect(ids).not.toContain('A') // self
      expect(ids).not.toContain('C') // would cycle (C needs A)
    })
  })

  describe('hasAnyJobNeeds', () => {
    it('detects intra-stage deps', () => {
      expect(hasAnyJobNeeds(fan)).toBe(true)
      expect(hasAnyJobNeeds([job('A'), job('B')])).toBe(false)
    })
  })

  describe('layoutJobs', () => {
    it('ranks serial chains horizontally, parallel jobs into lanes', () => {
      const { positions, ranks, lanes } = layoutJobs(fan)
      expect(positions.get('A')?.rank).toBe(0)
      expect(positions.get('B')?.rank).toBe(0)
      expect(positions.get('C')?.rank).toBe(1) // after A & B
      // A and B share rank 0 → different lanes (parallel)
      expect(positions.get('A')?.lane).not.toBe(positions.get('B')?.lane)
      expect(ranks).toBe(2)
      expect(lanes).toBe(2) // rank 0 has two lanes
    })
    it('handles a flat list (all roots) as a single rank', () => {
      const { ranks, lanes } = layoutJobs([job('A'), job('B'), job('C')])
      expect(ranks).toBe(1)
      expect(lanes).toBe(3)
    })
    it('ignores needs referencing jobs outside the stage (defensive)', () => {
      const { positions, ranks } = layoutJobs([job('A', ['ghost'])])
      expect(positions.get('A')?.rank).toBe(0)
      expect(ranks).toBe(1)
    })
  })
})
