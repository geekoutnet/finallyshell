package app

import (
	"slices"
)

// Several conversations at once: each conversation open in an AI window
// has a desktopAIState of its own, with its draft, the question it is
// answering, the commands waiting for the user and its status. The window
// shows one (window.state); the others go on answering behind it, their
// rows in the list marked, and a click on one shows it as it is. Starting
// a conversation, or opening another, never stops one that answers; the
// history stays open, and any conversation but one answering deletes.

// aiBusy reports whether the conversation in st answers a question, or
// waits for the user to allow a command.
func aiBusy(st *desktopAIState) bool { return st.cancel != nil || st.run != nil || st.confirm != nil }

// aiConvs are the conversations open in window, the one it shows among
// them.
func (a *App) aiConvs(window *desktopWindow) []*desktopAIState {
	convs := a.ai.convs[window]
	if cur, ok := window.state.(*desktopAIState); ok && !slices.Contains(convs, cur) {
		convs = append(convs, cur)
		if a.ai.convs == nil {
			a.ai.convs = map[*desktopWindow][]*desktopAIState{}
		}
		a.ai.convs[window] = convs
	}
	return convs
}

// aiConvOf is the conversation of window that has chat open, nil for none.
func (a *App) aiConvOf(window *desktopWindow, chat *desktopAIChat) *desktopAIState {
	if chat == nil {
		return nil
	}
	for _, st := range a.aiConvs(window) {
		if st.chat == chat {
			return st
		}
	}
	return nil
}

// aiChatBusy reports whether a conversation of any window answers in chat.
func (a *App) aiChatBusy(chat *desktopAIChat) bool {
	for _, convs := range a.ai.convs {
		for _, st := range convs {
			if st.chat == chat && aiBusy(st) {
				return true
			}
		}
	}
	return false
}

// desktopAIOpenChat shows chat in window, nil for a new conversation:
// where it is open already; else in the conversation shown, when that one
// is idle; else in a new one, the busy one answering on behind it.
func (a *App) desktopAIOpenChat(window *desktopWindow, chat *desktopAIChat) {
	cur, ok := window.state.(*desktopAIState)
	if !ok {
		return
	}
	if st := a.aiConvOf(window, chat); st != nil {
		a.aiShowConv(window, st)
		return
	}
	if chat == nil && cur.chat == nil && !aiBusy(cur) {
		return // a new conversation already
	}
	if !aiBusy(cur) {
		a.desktopAISelect(window.session, cur, chat)
		return
	}
	st := &desktopAIState{}
	a.aiShowConv(window, st)
	a.desktopAISelect(window.session, st, chat)
}

// aiShowConv shows st in window, with the window's own choices (the list
// beside it, the field's height), and lets go of the conversations neither
// shown nor busy, nor holding a question being typed.
func (a *App) aiShowConv(window *desktopWindow, st *desktopAIState) {
	convs := a.aiConvs(window)
	if cur, ok := window.state.(*desktopAIState); ok && cur != st {
		st.sidebar, st.inputH = cur.sidebar, cur.inputH
	}
	window.state = st
	keep := []*desktopAIState{}
	for _, other := range convs {
		if other == st || aiBusy(other) || other.chat != nil && other.draft != "" {
			keep = append(keep, other)
		}
	}
	if !slices.Contains(keep, st) {
		keep = append(keep, st)
	}
	a.ai.convs[window] = keep
}
