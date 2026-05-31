import { describe, it, expect } from 'vitest'
import {
  type StepBlock,
  shellQuote,
  shellUnquote,
  stepToCommandLines,
  compileSteps,
  conditionGuardLine,
  lineToStep,
  parseSteps,
  configUsesTemplate,
  nextStepId,
  STEP_KIND_META,
} from './stepCompile'

function step(partial: Partial<StepBlock> & { kind: StepBlock['kind'] }): StepBlock {
  return { id: nextStepId(), ...partial }
}

describe('stepCompile', () => {
  describe('shellQuote / shellUnquote round-trip', () => {
    const cases = ['hello', 'a b c', "it's", 'has "quotes"', '$VAR && rm -rf', '', "a'b'c"]
    for (const v of cases) {
      it(`round-trips ${JSON.stringify(v)}`, () => {
        expect(shellUnquote(shellQuote(v))).toBe(v)
      })
    }

    it('quotes wrap the value in single quotes', () => {
      expect(shellQuote('abc')).toBe("'abc'")
    })

    it('unquote returns bare tokens unchanged (not single-quoted)', () => {
      expect(shellUnquote('plain')).toBe('plain')
    })
  })

  describe('stepToCommandLines', () => {
    it('splits a multiline command step, dropping blank lines', () => {
      expect(stepToCommandLines(step({ kind: 'command', command: 'npm ci\n\nnpm run build' }))).toEqual([
        'npm ci',
        'npm run build',
      ])
    })

    it('compiles env into an export line with a quoted value', () => {
      expect(stepToCommandLines(step({ kind: 'env', envKey: 'NODE_ENV', envValue: 'production' }))).toEqual([
        "export NODE_ENV='production'",
      ])
    })

    it('drops env steps with an empty key', () => {
      expect(stepToCommandLines(step({ kind: 'env', envKey: '', envValue: 'x' }))).toEqual([])
    })

    it('compiles workDir into a cd line', () => {
      expect(stepToCommandLines(step({ kind: 'workDir', dir: 'frontend' }))).toEqual(["cd 'frontend'"])
    })

    it('artifact steps produce no command lines', () => {
      expect(stepToCommandLines(step({ kind: 'artifact', artifact: 'dist' }))).toEqual([])
    })
  })

  describe('compileSteps', () => {
    it('compiles an ordered mix into commands + artifactPath', () => {
      const steps: StepBlock[] = [
        step({ kind: 'env', envKey: 'CI', envValue: 'true' }),
        step({ kind: 'workDir', dir: 'frontend' }),
        step({ kind: 'command', command: 'npm ci\nnpm run build' }),
        step({ kind: 'artifact', artifact: 'frontend/dist' }),
        step({ kind: 'artifact', artifact: '*.log' }),
      ]
      const out = compileSteps(steps)
      expect(out.commands).toBe("export CI='true'\ncd 'frontend'\nnpm ci\nnpm run build")
      expect(out.artifactPath).toBe('frontend/dist\n*.log')
    })

    it('preserves step ordering (cd before the command that depends on it)', () => {
      const out = compileSteps([
        step({ kind: 'workDir', dir: 'api' }),
        step({ kind: 'command', command: 'go build ./...' }),
      ])
      expect(out.commands.split('\n')).toEqual(["cd 'api'", 'go build ./...'])
    })

    it('yields empty strings for no steps', () => {
      expect(compileSteps([])).toEqual({ commands: '', artifactPath: '' })
    })
  })

  describe('lineToStep', () => {
    it('classifies an export line as env', () => {
      const s = lineToStep("export TOKEN='abc'")
      expect(s.kind).toBe('env')
      expect(s.envKey).toBe('TOKEN')
      expect(s.envValue).toBe('abc')
    })

    it('classifies a cd line as workDir', () => {
      const s = lineToStep("cd 'frontend'")
      expect(s.kind).toBe('workDir')
      expect(s.dir).toBe('frontend')
    })

    it('falls back to command for anything else (lossless, keeps the raw line)', () => {
      const s = lineToStep('npm run build && echo done')
      expect(s.kind).toBe('command')
      expect(s.command).toBe('npm run build && echo done')
    })

    it('does not misread an unquoted cd without single quotes', () => {
      const s = lineToStep('cd frontend')
      expect(s.kind).toBe('workDir')
      expect(s.dir).toBe('frontend')
    })
  })

  describe('parseSteps', () => {
    it('reverses compileSteps for a representative config (round-trip)', () => {
      const original: StepBlock[] = [
        step({ kind: 'env', envKey: 'NODE_ENV', envValue: 'production' }),
        step({ kind: 'workDir', dir: 'frontend' }),
        step({ kind: 'command', command: 'npm ci' }),
        step({ kind: 'command', command: 'npm run build' }),
        step({ kind: 'artifact', artifact: 'frontend/dist' }),
      ]
      const compiled = compileSteps(original)
      const parsed = parseSteps({ commands: compiled.commands, artifactPath: compiled.artifactPath })

      // ids differ, compare structurally
      const shape = (s: StepBlock) => ({
        kind: s.kind,
        command: s.command,
        envKey: s.envKey,
        envValue: s.envValue,
        dir: s.dir,
        artifact: s.artifact,
      })
      expect(parsed.map(shape)).toEqual(original.map(shape))
    })

    it('splits multiline commands into one step per line (lossy by design)', () => {
      const parsed = parseSteps({ commands: 'echo a\necho b\necho c' })
      expect(parsed.map((s) => s.command)).toEqual(['echo a', 'echo b', 'echo c'])
    })

    it('parses artifactPath lines into artifact steps', () => {
      const parsed = parseSteps({ commands: '', artifactPath: 'dist\n*.jar' })
      expect(parsed.map((s) => [s.kind, s.artifact])).toEqual([
        ['artifact', 'dist'],
        ['artifact', '*.jar'],
      ])
    })

    it('returns an empty list for empty config (caller decides fallback)', () => {
      expect(parseSteps({})).toEqual([])
    })

    it('preserves complex command lines without mangling them', () => {
      const cmd = 'for f in *.go; do gofmt -l "$f"; done'
      const parsed = parseSteps({ commands: cmd })
      expect(parsed).toHaveLength(1)
      expect(parsed[0].kind).toBe('command')
      expect(parsed[0].command).toBe(cmd)
    })
  })

  describe('condition guard (set -e safe)', () => {
    it('compiles to a single-line if-guard that exits 0 when condition fails', () => {
      const line = conditionGuardLine('[ "$BRANCH" = "main" ]')
      expect(line).toBe(
        `if ! ( [ "$BRANCH" = "main" ] ); then echo '条件不成立,跳过后续步骤'; exit 0; fi`,
      )
      // 单行(逐行 round-trip 不破裂)。
      expect(line.includes('\n')).toBe(false)
      // 条件在 if 测试上下文(set -e 豁免),命中走 exit 0(干净早退)。
      expect(line.startsWith('if ! (')).toBe(true)
      expect(line.includes('; exit 0; fi')).toBe(true)
    })

    it('stepToCommandLines emits one guard line; empty condition → none', () => {
      expect(stepToCommandLines(step({ kind: 'condition', condition: 'test -f package.json' }))).toEqual([
        `if ! ( test -f package.json ); then echo '条件不成立,跳过后续步骤'; exit 0; fi`,
      ])
      expect(stepToCommandLines(step({ kind: 'condition', condition: '   ' }))).toEqual([])
    })

    it('flattens a multiline condition to a single line', () => {
      expect(stepToCommandLines(step({ kind: 'condition', condition: '[ -d dist ]\n' }))).toEqual([
        `if ! ( [ -d dist ] ); then echo '条件不成立,跳过后续步骤'; exit 0; fi`,
      ])
    })

    it('lineToStep parses a guard line back to a condition step (round-trip)', () => {
      const cond = '[ "$BRANCH" = "main" ] && [ -f deploy.sh ]'
      const back = lineToStep(conditionGuardLine(cond))
      expect(back.kind).toBe('condition')
      expect(back.condition).toBe(cond)
    })

    it('round-trips a condition step through compile + parse', () => {
      const steps: StepBlock[] = [
        step({ kind: 'condition', condition: 'test -f package.json' }),
        step({ kind: 'command', command: 'npm ci' }),
      ]
      const compiled = compileSteps(steps)
      const parsed = parseSteps({ commands: compiled.commands })
      expect(parsed.map((s) => s.kind)).toEqual(['condition', 'command'])
      expect(parsed[0].condition).toBe('test -f package.json')
      expect(parsed[1].command).toBe('npm ci')
    })

    it('a plain command is not misclassified as a condition', () => {
      expect(lineToStep('echo hello').kind).toBe('command')
      expect(lineToStep('if true; then echo hi; fi').kind).toBe('command')
    })
  })

  describe('configUsesTemplate', () => {
    it('detects commandTemplate / params', () => {
      expect(configUsesTemplate({ commandTemplate: 'cd {{dir}}' })).toBe(true)
      expect(configUsesTemplate({ params: 'dir=frontend' })).toBe(true)
    })

    it('is false for plain script config', () => {
      expect(configUsesTemplate({ commands: 'npm ci', image: 'node:20' })).toBe(false)
      expect(configUsesTemplate({})).toBe(false)
    })
  })

  it('STEP_KIND_META covers every kind', () => {
    expect(Object.keys(STEP_KIND_META).sort()).toEqual(['artifact', 'command', 'condition', 'env', 'workDir'])
  })
})
