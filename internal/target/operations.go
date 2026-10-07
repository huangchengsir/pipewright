package target

import (
	"errors"
	"regexp"
)

var (
	systemdUnitPattern  = regexp.MustCompile(`^[\w][\w.@-]*$`)
	dockerTargetPattern = regexp.MustCompile(`^[\w][\w.-]*$`)
)

// ValidateServiceParams preserves the existing manual HTTP contract, not opschat permissions.
func ValidateServiceParams(typ, tgt, action string) error {
	if tgt == "" {
		return errors.New("target 不能为空")
	}
	if len(tgt) > 256 {
		return errors.New("target 过长")
	}
	switch typ {
	case "systemd":
		if action != "restart" && action != "stop" && action != "start" {
			return errors.New("非法 action(systemd 仅支持 restart/stop/start)")
		}
		if !systemdUnitPattern.MatchString(tgt) {
			return errors.New("非法 systemd unit 名")
		}
	case "docker":
		switch action {
		case "restart", "stop", "start", "pause", "unpause", "kill", "rm":
		default:
			return errors.New("非法 action(docker 仅支持 restart/stop/start/pause/unpause/kill/rm)")
		}
		if !dockerTargetPattern.MatchString(tgt) {
			return errors.New("非法 docker 容器名")
		}
	default:
		return errors.New("非法 type(仅支持 systemd/docker)")
	}
	return nil
}

// ValidateOpsAction excludes the existing manual kill/rm operations.
func ValidateOpsAction(typ, tgt, action string) error {
	switch action {
	case "start", "stop", "restart":
	case "pause", "unpause":
		if typ != "docker" {
			return errors.New("target: unsupported ops action")
		}
	default:
		return errors.New("target: unsupported ops action")
	}
	return ValidateServiceParams(typ, tgt, action)
}

func BuildServiceCmd(typ, action, tgt string) []string {
	switch typ {
	case "systemd":
		return []string{"systemctl", action, tgt}
	case "docker":
		return []string{"docker", action, tgt}
	}
	return nil
}
