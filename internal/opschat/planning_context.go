package opschat

import (
	"bufio"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/huangchengsir/pipewright/internal/target"
)

const containerObservationBytes = 8 << 10

func (s *Service) containerObservations(ctx context.Context, id string, targets []target.ConnectionSnapshot) ([]ContainerObservation, error) {
	if len(targets) > 8 {
		return nil, ErrInvalid
	}
	out := make([]ContainerObservation, len(targets))
	now := time.Now().UTC()
	for i, snapshot := range targets {
		observation := ContainerObservation{TargetAlias: "target_" + strconv.Itoa(i), Containers: []ContainerFact{}}
		call, unavailable, err := s.recentContainerCall(ctx, id, snapshot, now)
		if err != nil {
			return nil, err
		}
		observation.Unavailable = unavailable
		if call != nil {
			collected := *call.CollectedAt
			observation.CollectedAt = &collected
			observation.Truncated = call.Truncated
			s.projectContainerRows(&observation, call.Output)
		}
		out[i] = observation
	}
	// Drop whole facts, not IDs, and retain an explicit marker for every affected target.
	for {
		encoded, err := json.Marshal(out)
		if err != nil {
			return nil, ErrInvalid
		}
		if len(encoded) <= containerObservationBytes {
			return out, nil
		}
		removed := false
		for i := len(out) - 1; i >= 0; i-- {
			if n := len(out[i].Containers); n > 0 {
				out[i].Containers = out[i].Containers[:n-1]
				out[i].Truncated = true
				removed = true
				break
			}
		}
		if !removed {
			return nil, ErrInvalid
		}
	}
}

func (s *Service) recentContainerCall(ctx context.Context, id string, snapshot target.ConnectionSnapshot, now time.Time) (*Call, string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM ops_chat_calls
		WHERE session_id=? AND server_id=? AND tool_id=? AND status=?
		ORDER BY created_at DESC,id DESC LIMIT 8`, id, snapshot.Server.ID, "docker_containers", Succeeded)
	if err != nil {
		return nil, "", ErrStorage
	}
	defer rows.Close()
	fingerprint := snapshot.Fingerprint()
	unavailable := "no_results"
	var latest *Call
	for rows.Next() {
		var encrypted []byte
		if rows.Scan(&encrypted) != nil {
			return nil, "", ErrStorage
		}
		var body callBody
		if err := s.open(encrypted, &body); err != nil {
			return nil, "", err
		}
		call := body.View
		if call.SessionID != id || call.ServerID != snapshot.Server.ID || call.ToolID != "docker_containers" || call.ExitCode != nil && *call.ExitCode != 0 {
			continue
		}
		if call.TargetHash != fingerprint || body.Snapshot.Fingerprint() != fingerprint {
			if unavailable == "no_results" {
				unavailable = "connection_changed"
			}
			continue
		}
		if call.CollectedAt == nil || call.CollectedAt.Before(now.Add(-15*time.Minute)) || call.CollectedAt.After(now) {
			unavailable = "expired"
			continue
		}
		if latest == nil || call.CollectedAt.After(*latest.CollectedAt) {
			call.Truncated = call.Truncated || body.ReadTruncated
			latest = &call
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", ErrStorage
	}
	if latest != nil {
		return latest, "", nil
	}
	return nil, unavailable, nil
}

func (s *Service) projectContainerRows(observation *ContainerObservation, output string) {
	if len(output) > 64<<10 {
		observation.Truncated = true
		return
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 4096), (64<<10)+1)
	seen := map[string]bool{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var fields map[string]json.RawMessage
		if !utf8.ValidString(line) || strictWithLimit([]byte(line), &fields, 64<<10) != nil {
			observation.Truncated = true
			continue
		}
		var fact ContainerFact
		valid := true
		for _, field := range []struct {
			key string
			dst *string
		}{{"id", &fact.ID}, {"names", &fact.Name}, {"image", &fact.Image}, {"state", &fact.State}} {
			raw := fields[field.key]
			if !validJSONStringUnicode(raw) || json.Unmarshal(raw, field.dst) != nil {
				valid = false
				break
			}
		}
		if !valid || !immutableContainer.MatchString(fact.ID) || s.scrub(fact.ID, 64) != fact.ID || seen[fact.ID] {
			observation.Truncated = true
			continue
		}
		if len(fact.Name) > 256 {
			valid = false
		}
		for _, value := range []string{fact.Name, fact.Image, fact.State} {
			if !utf8.ValidString(value) {
				valid = false
			}
			for _, r := range value {
				if unicode.IsControl(r) {
					valid = false
					break
				}
			}
		}
		// Names identify candidates: never shorten them to satisfy a projection limit.
		name := s.masker.ScrubTruncated(fact.Name)
		if !valid || len(name) > 256 {
			observation.Truncated = true
			continue
		}
		if name != fact.Name {
			observation.Truncated = true
		}
		fact.Name = name
		if len(observation.Containers) == 20 {
			observation.Truncated = true
			continue
		}
		for _, field := range []struct {
			value *string
			limit int
		}{{&fact.Image, 512}, {&fact.State, 128}} {
			projected := s.scrub(*field.value, field.limit)
			if projected != *field.value {
				observation.Truncated = true
			}
			*field.value = projected
		}
		seen[fact.ID] = true
		observation.Containers = append(observation.Containers, fact)
	}
	if scanner.Err() != nil {
		observation.Truncated = true
	}
}
