package dbmigration

import "testing"

func TestColumnInventoryAllowsReorderedDDLButRejectsDataLoss(t *testing.T) {
	expected := []string{"id", "ciphertext", "name"}
	if !sameColumnInventory([]string{"ciphertext", "name", "id"}, expected) {
		t.Fatal("physical reorder rejected")
	}
	for _, actual := range [][]string{{"id", "name"}, {"id", "name", "extra"}, {"id", "name", "name"}, {"id", "ciphertext", "name", "extra"}} {
		if sameColumnInventory(actual, expected) {
			t.Fatal("inexact inventory accepted", actual)
		}
	}
}
