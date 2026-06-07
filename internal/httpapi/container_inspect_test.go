package httpapi

import "testing"

// 一段贴近真实 `docker inspect <name>` 的输出片段(JSON 数组,仅保留我们关心的字段 + 少量噪声字段)。
const sampleInspectJSON = `[
  {
    "Id": "9f1e2c3d4b5a6789abcdef0123456789abcdef0123456789abcdef0123456789",
    "Created": "2026-06-01T08:30:00.123456789Z",
    "Path": "docker-entrypoint.sh",
    "Args": ["nginx", "-g", "daemon off;"],
    "State": {
      "Status": "running",
      "Running": true,
      "Pid": 12345
    },
    "Config": {
      "Image": "nginx:1.27-alpine",
      "Cmd": ["nginx", "-g", "daemon off;"],
      "Env": [
        "PATH=/usr/local/sbin:/usr/local/bin",
        "NGINX_VERSION=1.27.0",
        "DB_PASSWORD=s3cr3t"
      ],
      "Labels": {
        "com.example.team": "platform",
        "maintainer": "ops@example.com"
      }
    },
    "HostConfig": {
      "RestartPolicy": {
        "Name": "unless-stopped",
        "MaximumRetryCount": 0
      }
    },
    "Mounts": [
      {
        "Type": "bind",
        "Source": "/data/nginx/conf",
        "Destination": "/etc/nginx/conf.d",
        "Mode": "ro",
        "RW": false
      },
      {
        "Type": "volume",
        "Name": "nginx-logs",
        "Source": "/var/lib/docker/volumes/nginx-logs/_data",
        "Destination": "/var/log/nginx",
        "Mode": "z",
        "RW": true
      }
    ],
    "NetworkSettings": {
      "Networks": {
        "bridge": {
          "IPAddress": "172.17.0.2",
          "Gateway": "172.17.0.1"
        },
        "app-net": {
          "IPAddress": "10.5.0.7"
        }
      },
      "Ports": {
        "80/tcp": [
          {"HostIp": "0.0.0.0", "HostPort": "8080"}
        ],
        "443/tcp": null
      }
    }
  }
]`

func TestParseContainerInspect_SelectedFields(t *testing.T) {
	dto, err := parseContainerInspect(sampleInspectJSON)
	if err != nil {
		t.Fatalf("parseContainerInspect 失败: %v", err)
	}

	if !dto.Reachable {
		t.Errorf("Reachable = false, 期望 true")
	}
	if dto.Image != "nginx:1.27-alpine" {
		t.Errorf("Image = %q, 期望 nginx:1.27-alpine", dto.Image)
	}
	if dto.Command != "nginx -g daemon off;" {
		t.Errorf("Command = %q, 期望 'nginx -g daemon off;'", dto.Command)
	}
	if dto.CreatedAt != "2026-06-01T08:30:00.123456789Z" {
		t.Errorf("CreatedAt = %q", dto.CreatedAt)
	}
	if dto.State != "running" {
		t.Errorf("State = %q, 期望 running", dto.State)
	}
	if dto.RestartPolicy != "unless-stopped" {
		t.Errorf("RestartPolicy = %q, 期望 unless-stopped", dto.RestartPolicy)
	}

	// 环境变量原样保留(含密钥值 —— 后端不特殊处理,展示给已登录管理员)。
	if len(dto.Env) != 3 {
		t.Fatalf("Env 长度 = %d, 期望 3", len(dto.Env))
	}
	if dto.Env[2] != "DB_PASSWORD=s3cr3t" {
		t.Errorf("Env[2] = %q, 期望 DB_PASSWORD=s3cr3t", dto.Env[2])
	}

	// 挂载。
	if len(dto.Mounts) != 2 {
		t.Fatalf("Mounts 长度 = %d, 期望 2", len(dto.Mounts))
	}
	m0 := dto.Mounts[0]
	if m0.Source != "/data/nginx/conf" || m0.Destination != "/etc/nginx/conf.d" || m0.Mode != "ro" || m0.RW {
		t.Errorf("Mounts[0] = %+v, 不符合预期", m0)
	}
	if !dto.Mounts[1].RW {
		t.Errorf("Mounts[1].RW = false, 期望 true")
	}

	// 网络按名排序:app-net < bridge。
	if len(dto.Networks) != 2 {
		t.Fatalf("Networks 长度 = %d, 期望 2", len(dto.Networks))
	}
	if dto.Networks[0].Name != "app-net" || dto.Networks[0].IPAddress != "10.5.0.7" {
		t.Errorf("Networks[0] = %+v, 期望 app-net/10.5.0.7", dto.Networks[0])
	}
	if dto.Networks[1].Name != "bridge" || dto.Networks[1].IPAddress != "172.17.0.2" {
		t.Errorf("Networks[1] = %+v, 期望 bridge/172.17.0.2", dto.Networks[1])
	}

	// 端口按键排序:443/tcp(未发布,占位)< 80/tcp(发布到 0.0.0.0:8080)。
	if len(dto.Ports) != 2 {
		t.Fatalf("Ports 长度 = %d, 期望 2", len(dto.Ports))
	}
	if dto.Ports[0].ContainerPort != "443/tcp" || dto.Ports[0].HostPort != "" {
		t.Errorf("Ports[0] = %+v, 期望 443/tcp 无绑定", dto.Ports[0])
	}
	if dto.Ports[1].ContainerPort != "80/tcp" || dto.Ports[1].HostIP != "0.0.0.0" || dto.Ports[1].HostPort != "8080" {
		t.Errorf("Ports[1] = %+v, 期望 80/tcp -> 0.0.0.0:8080", dto.Ports[1])
	}

	// 标签。
	if dto.Labels["com.example.team"] != "platform" {
		t.Errorf("Labels[com.example.team] = %q, 期望 platform", dto.Labels["com.example.team"])
	}
}

// 命令回退:无 Config.Cmd 时用 Path + Args 拼接。
func TestParseContainerInspect_CommandFallback(t *testing.T) {
	raw := `[{"Path":"/bin/myapp","Args":["--port","9000"],"Config":{"Image":"busybox"},"State":{"Status":"exited"}}]`
	dto, err := parseContainerInspect(raw)
	if err != nil {
		t.Fatalf("parseContainerInspect 失败: %v", err)
	}
	if dto.Command != "/bin/myapp --port 9000" {
		t.Errorf("Command = %q, 期望 '/bin/myapp --port 9000'", dto.Command)
	}
	if dto.State != "exited" {
		t.Errorf("State = %q, 期望 exited", dto.State)
	}
	// 空集合应序列化为非 nil 切片/映射(前端不必判 null)。
	if dto.Env == nil || dto.Mounts == nil || dto.Networks == nil || dto.Ports == nil || dto.Labels == nil {
		t.Errorf("空集合不应为 nil: env=%v mounts=%v networks=%v ports=%v labels=%v",
			dto.Env, dto.Mounts, dto.Networks, dto.Ports, dto.Labels)
	}
}

// 空数组 / 非法 JSON → 错误(handler 据此回 reachable:false)。
func TestParseContainerInspect_Errors(t *testing.T) {
	if _, err := parseContainerInspect("[]"); err == nil {
		t.Errorf("空数组应返回错误")
	}
	if _, err := parseContainerInspect("not-json"); err == nil {
		t.Errorf("非法 JSON 应返回错误")
	}
}
