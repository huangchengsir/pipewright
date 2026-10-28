package configstate

import "sync"

// Reader evidence is process-local and is never reconstructed from a heartbeat,
// a client assertion, or persisted rows. Only the runtime calls these functions
// at actual admission and after its final reader reference has exited.
type readerEvidence struct {
	owner, evidence string
	active, retired bool
}

var localReaders = struct {
	sync.Mutex
	items map[string]*readerEvidence
}{items: make(map[string]*readerEvidence)}

// StartActivityReaders invalidates any old exit proof before a runtime is
// allowed to read configuration. A recovered owner can never start again.
func StartActivityReaders(id, owner string) error {
	localReaders.Lock()
	defer localReaders.Unlock()
	if v := localReaders.items[id]; v != nil {
		if v.owner != owner || v.retired {
			return ErrOwner
		}
		v.active = true
		v.evidence = ""
		return nil
	}
	localReaders.items[id] = &readerEvidence{owner: owner, active: true}
	return nil
}

// RecordActivityReadersExited must be called only after the worker and every
// asynchronous reader have returned. It provides recovery evidence if the
// subsequent ordinary SQL release fails; it does not itself release a hold.
func RecordActivityReadersExited(id, owner string) {
	localReaders.Lock()
	defer localReaders.Unlock()
	v := localReaders.items[id]
	if v == nil || v.owner != owner || v.retired {
		return
	}
	v.active = false
	if v.evidence == "" {
		v.evidence = newEvidenceID()
	}
}

func ForgetActivityReaders(id, owner string) {
	localReaders.Lock()
	defer localReaders.Unlock()
	if v := localReaders.items[id]; v != nil && v.owner == owner && !v.retired {
		delete(localReaders.items, id)
	}
}

// ActivityReadersExited exposes only a boolean runtime fact, not an evidence
// token. A database row, elapsed time or a new process cannot manufacture it.
func ActivityReadersExited(id, owner string) bool {
	localReaders.Lock()
	defer localReaders.Unlock()
	v := localReaders.items[id]
	return v != nil && v.owner == owner && !v.active && v.evidence != ""
}

func readerEvidenceSnapshot() map[string]readerEvidence {
	localReaders.Lock()
	defer localReaders.Unlock()
	out := make(map[string]readerEvidence, len(localReaders.items))
	for k, v := range localReaders.items {
		out[k] = *v
	}
	return out
}
