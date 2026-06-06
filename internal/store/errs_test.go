package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestIsUniqueErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sqlite text", errors.New("UNIQUE constraint failed: projects.name"), true},
		{"sqlite lower", errors.New("unique constraint failed"), true},
		{"mysql 1062", &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}, true},
		{"mysql wrapped", fmt.Errorf("upsert: %w", &mysql.MySQLError{Number: 1062}), true},
		{"mysql fk code not unique", &mysql.MySQLError{Number: 1452}, false},
		{"unrelated", errors.New("boom"), false},
	}
	for _, c := range cases {
		if got := IsUniqueErr(c.err); got != c.want {
			t.Errorf("%s: IsUniqueErr=%v want %v", c.name, got, c.want)
		}
	}
}

func TestIsForeignKeyErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sqlite text", errors.New("FOREIGN KEY constraint failed"), true},
		{"sqlite lower", errors.New("foreign key constraint failed"), true},
		{"mysql 1451", &mysql.MySQLError{Number: 1451}, true},
		{"mysql 1452", &mysql.MySQLError{Number: 1452}, true},
		{"mysql wrapped", fmt.Errorf("insert: %w", &mysql.MySQLError{Number: 1451}), true},
		{"mysql unique code not fk", &mysql.MySQLError{Number: 1062}, false},
		{"unrelated", errors.New("boom"), false},
	}
	for _, c := range cases {
		if got := IsForeignKeyErr(c.err); got != c.want {
			t.Errorf("%s: IsForeignKeyErr=%v want %v", c.name, got, c.want)
		}
	}
}
