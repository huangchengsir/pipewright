import { describe, it, expect } from 'vitest'
import { parseDockerRun } from './dockerRun'

describe('parseDockerRun', () => {
  it('parses a full docker run', () => {
    const p = parseDockerRun(
      'docker run -d --name web -p 8080:80 -p 443:443 -e TZ=Asia/Shanghai -v /data:/usr/share/nginx/html:ro --restart unless-stopped nginx:latest nginx -g "daemon off;"',
    )
    expect(p).not.toBeNull()
    expect(p!.name).toBe('web')
    expect(p!.image).toBe('nginx:latest')
    expect(p!.ports).toEqual(['8080:80', '443:443'])
    expect(p!.env).toEqual(['TZ=Asia/Shanghai'])
    expect(p!.volumes).toEqual(['/data:/usr/share/nginx/html:ro'])
    expect(p!.restart).toBe('unless-stopped')
    expect(p!.command).toContain('nginx -g')
  })

  it('supports --flag=value form and sudo prefix', () => {
    const p = parseDockerRun('sudo docker run --name=cache --restart=always redis:latest')
    expect(p!.name).toBe('cache')
    expect(p!.restart).toBe('always')
    expect(p!.image).toBe('redis:latest')
  })

  it('handles minimal run', () => {
    const p = parseDockerRun('docker run nginx')
    expect(p!.image).toBe('nginx')
    expect(p!.ports).toEqual([])
  })

  it('ignores boolean flags (-it, --rm)', () => {
    const p = parseDockerRun('docker run -it --rm alpine sh')
    expect(p!.image).toBe('alpine')
    expect(p!.command).toBe('sh')
  })

  it('returns null for non docker-run', () => {
    expect(parseDockerRun('ls -la')).toBeNull()
    expect(parseDockerRun('docker ps')).toBeNull()
  })
})
