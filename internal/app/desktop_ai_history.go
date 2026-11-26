package app

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"finallyshell/internal/config"
)

func (a *App) aiLoadChats() {
	if a.ai.loaded || a.ai.loading {
		return
	}
	a.ai.loading = true
	file := filepath.Join(config.Dir(), "chats.json")
	go func() {
		chats, err := readAIChats(file)
		a.post(func() {
			a.ai.loading, a.ai.loaded = false, true
			a.ai.chats, a.ai.storeError = chats, err != nil
		})
	}()
}

func readAIChats(file string) ([]*desktopAIChat, error) {
	handle, err := os.Open(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	var chats []*desktopAIChat
	if err := json.NewDecoder(io.LimitReader(handle, 96<<20)).Decode(&chats); err != nil {
		return nil, err
	}
	var out []*desktopAIChat
	for _, chat := range chats[:min(aiMaxChats, len(chats))] {
		if chat == nil {
			continue
		}
		var messages []*desktopAIMessage
		for _, message := range chat.Messages[max(0, len(chat.Messages)-aiMaxMessages):] {
			if message == nil || len(message.Text) > aiReplyLimit {
				continue
			}
			switch message.Role {
			case "command":
				// A command the app quit in the middle of did not finish.
				if !message.Status.finished() || message.Status > aiCmdFailed {
					message.Status = aiCmdCancelled
				}
				if message.Status != aiCmdDone {
					message.Exit = nil
				}
				messages = append(messages, message)
			case "assistant", "user":
				messages = append(messages, message)
			}
		}
		chat.Messages = messages
		out = append(out, chat)
	}
	return out, nil
}

func (a *App) aiSaveChats() {
	if a.ai.storeError && !a.ai.saved {
		return
	}
	var snapshot []desktopAIChat
	for _, chat := range a.ai.chats {
		copy := desktopAIChat{Host: chat.Host, Title: chat.Title, Model: chat.Model}
		for _, message := range chat.Messages {
			if message.Text != "" && !message.private {
				// A command's output stays in memory.
				copy.Messages = append(copy.Messages, &desktopAIMessage{Role: message.Role, Text: message.Text, Model: message.Model,
					Reason: message.Reason, Status: message.Status, Exit: message.Exit, Millis: message.Millis, Auto: message.Auto})
			}
		}
		if len(copy.Messages) != 0 {
			snapshot = append(snapshot, copy)
		}
	}
	file := filepath.Join(config.Dir(), "chats.json")
	a.ai.saved = true
	a.ai.disk.do(func() {
		err := writeAIChats(file, snapshot)
		a.post(func() { a.ai.storeError = err != nil })
	})
}

func writeAIChats(file string, snapshot []desktopAIChat) error {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	handle, err := os.CreateTemp(dir, ".chats-*")
	if err != nil {
		return err
	}
	defer os.Remove(handle.Name())
	encodeErr := json.NewEncoder(handle).Encode(snapshot)
	if err := errors.Join(encodeErr, handle.Close()); err != nil {
		return err
	}
	return os.Rename(handle.Name(), file)
}
