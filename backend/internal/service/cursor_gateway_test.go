package service

import "testing"

func TestShouldUseCursorProxy(t *testing.T) {
	if shouldUseCursorProxy(nil) {
		t.Fatal("nil")
	}
	sand := &Account{Platform: PlatformCursorSand}
	ide := &Account{Platform: PlatformCursor}
	grok := &Account{Platform: PlatformGrok}
	if !shouldUseCursorProxy(sand) || !shouldUseCursorProxy(ide) {
		t.Fatal("expected cursor platforms")
	}
	if shouldUseCursorProxy(grok) {
		t.Fatal("grok is not cursor proxy")
	}
	if sand.IsCursor() || ide.IsCursorSand() {
		t.Fatal("platforms must not overlap")
	}
}
