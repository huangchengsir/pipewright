package selfupdate

import (
	"errors"
	"os"
)

func (s *Service) openPrivate(name string) (*os.File, error) {
	st, e := s.root.Lstat(name)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	flags := os.O_RDWR
	if errors.Is(e, os.ErrNotExist) {
		flags |= os.O_CREATE | os.O_EXCL
	} else if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalid
	}
	f, e := s.root.OpenFile(name, flags, 0600)
	if e != nil {
		return nil, e
	}
	current, x := s.root.Lstat(name)
	info, y := f.Stat()
	if x != nil || y != nil || !current.Mode().IsRegular() || !os.SameFile(current, info) || !singleLink(f, info) {
		f.Close()
		return nil, ErrInvalid
	}
	return f, nil
}
