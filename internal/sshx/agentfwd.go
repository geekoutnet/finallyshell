package sshx

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type forwardedAgent struct {
	agent.Agent
	active atomic.Int32
}

var errForwardedAgent = errors.New("the forwarded agent only allows listing keys and signing while a shell is open")

func (a *forwardedAgent) List() ([]*agent.Key, error) {
	if a.active.Load() == 0 {
		return nil, errForwardedAgent
	}
	return a.Agent.List()
}

func (a *forwardedAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	return a.SignWithFlags(key, data, 0)
}

func (a *forwardedAgent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	if a.active.Load() == 0 {
		return nil, errForwardedAgent
	}
	return a.Agent.(agent.ExtendedAgent).SignWithFlags(key, data, flags)
}

func (*forwardedAgent) Extension(string, []byte) ([]byte, error) {
	return nil, agent.ErrExtensionUnsupported
}
func (*forwardedAgent) Add(agent.AddedKey) error   { return errForwardedAgent }
func (*forwardedAgent) Remove(ssh.PublicKey) error { return errForwardedAgent }
func (*forwardedAgent) RemoveAll() error           { return errForwardedAgent }
func (*forwardedAgent) Lock([]byte) error          { return errForwardedAgent }
func (*forwardedAgent) Unlock([]byte) error        { return errForwardedAgent }

var agentForwards struct {
	sync.Mutex
	clients map[*ssh.Client]*forwardedAgent
}

func forwardingAgent(client *ssh.Client, setting string) (*forwardedAgent, error) {
	agentForwards.Lock()
	defer agentForwards.Unlock()
	if existing := agentForwards.clients[client]; existing != nil {
		return existing, nil
	}
	local, conn, _, err := dialAgent(setting)
	if err != nil {
		return nil, err
	}
	forwarder := &forwardedAgent{Agent: local}
	if err := agent.ForwardToAgent(client, forwarder); err != nil {
		conn.Close()
		return nil, err
	}
	if agentForwards.clients == nil {
		agentForwards.clients = make(map[*ssh.Client]*forwardedAgent)
	}
	agentForwards.clients[client] = forwarder
	go func(conn io.Closer) {
		client.Wait()
		conn.Close()
		agentForwards.Lock()
		delete(agentForwards.clients, client)
		agentForwards.Unlock()
	}(conn)
	return forwarder, nil
}

func requestAgentForward(client *ssh.Client, session *ssh.Session, setting string) (func(), error) {
	forwarder, err := forwardingAgent(client, setting)
	if err != nil {
		return nil, err
	}
	if err := agent.RequestAgentForwarding(session); err != nil {
		return nil, err
	}
	forwarder.active.Add(1)
	var once sync.Once
	return func() { once.Do(func() { forwarder.active.Add(-1) }) }, nil
}
