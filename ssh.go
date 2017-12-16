package main

import (
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/mkishere/binutils"
	"github.com/mkishere/sshsyrup/util/termlogger"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/terminal"
)

// SSHSession stores SSH session info
type SSHSession struct {
	user          string
	src           net.Addr
	clientVersion string
	activity      chan bool
	sshChan       <-chan ssh.NewChannel
	ptyReq        *PtyRequest
	term          *terminal.Terminal
}

type EnvRequest struct {
	Name  string
	Value string
}

type PtyRequest struct {
	Term    string
	Width   uint32
	Height  uint32
	PWidth  uint32
	PHeight uint32
	Modes   []uint8
}
type WinChgRequest struct {
	Width  uint32
	Height uint32
}

func NewSSHSession(nConn net.Conn, sshConfig *ssh.ServerConfig, localConfig Config) (*SSHSession, error) {
	conn, chans, reqs, err := ssh.NewServerConn(nConn, sshConfig)
	if err != nil {
		return nil, err
	}

	log.Printf("New SSH connection from %s (%s)", conn.RemoteAddr(), conn.ClientVersion())

	activity := make(chan bool)
	go func(activity chan bool) {
		defer nConn.Close()
		for range activity {
			// When receive from activity channel, reset deadline
			nConn.SetReadDeadline(time.Now().Add(localConfig.Timeout))
		}
	}(activity)

	go ssh.DiscardRequests(reqs)
	return &SSHSession{
		user:          conn.User(),
		src:           conn.RemoteAddr(),
		clientVersion: string(conn.ClientVersion()),
		activity:      activity,
		sshChan:       chans,
	}, nil
}

func (s *SSHSession) handleNewSession(newChan ssh.NewChannel) {

	channel, requests, err := newChan.Accept()
	if err != nil {
		log.Printf("Could not accept channel: %v", err)
		return
	}

	defer func() {
		channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
		channel.Close()
	}()

	for req := range requests {
		switch req.Type {
		case "winadj@putty.projects.tartarus.org", "simple@putty.projects.tartarus.org":
			//Do nothing here
		case "pty-req":
			// Of coz we are not going to create a PTY here as we are honeypot.
			// We are creating a pseudo-PTY
			var ptyreq PtyRequest
			if err := ssh.Unmarshal(req.Payload, &ptyreq); err != nil {
				req.Reply(false, nil)
			}
			log.Printf("User [%v] requesting pty", s.user)
			s.ptyReq = &ptyreq
			req.Reply(true, nil)
		case "env":
			var envReq EnvRequest
			if err := ssh.Unmarshal(req.Payload, &envReq); err != nil {
				req.Reply(false, nil)
			} else {
				log.Printf("User [%v] sends envvar:%v=%v", s.user, envReq.Name, envReq.Value)
				req.Reply(true, nil)
			}
		case "shell":
			log.Printf("User [%v] requesting shell access", s.user)
			if s.ptyReq == nil {
				s.ptyReq = &PtyRequest{
					Width:  80,
					Height: 24,
					Term:   "vt100",
					Modes:  []byte{},
				}
			}
			s.NewShell(channel)
			req.Reply(true, nil)
		case "subsystem":
			var subsys string
			binutils.Unmarshal(req.Payload, &s)
			log.Printf("User [%v] requested subsystem %v", s.user, subsys)
			req.Reply(true, nil)
		case "window-change":
			if s.term == nil {
				req.Reply(false, nil)
			} else {
				var winChg *WinChgRequest
				if err := ssh.Unmarshal(req.Payload, winChg); err != nil {
					req.Reply(false, nil)
				}
				s.term.SetSize(int(winChg.Width), int(winChg.Height))
				req.Reply(true, nil)
			}
		default:
			log.Printf("Unknown channel request type %v", req.Type)
		}
	}
}

func (s *SSHSession) handleNewConn() {
	// Service the incoming Channel channel.
	for newChannel := range s.sshChan {
		// Channels have a type, depending on the application level
		// protocol intended. In the case of a shell, the type is
		// "session" and ServerShell may be used to present a simple
		// terminal interface.
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			log.Printf("Unknown channel type %v", newChannel.ChannelType())
			continue
		} else {
			go s.handleNewSession(newChannel)
		}
		/* for {
			select {
			case s := <-cmd:

				createShell(channel)
			case s := <-subsystem:
				subsys := strings.TrimSpace(s)
				log.Printf("[%v] requesting subsystem \"%v\"", perms.Extensions["user"], subsys)
				if subsys == "sftp" {

				}
			}
		} */
	}
}

// NewShell creates new shell
func (s *SSHSession) NewShell(channel ssh.Channel) {
	tLog := termlogger.NewACastLogger(int(s.ptyReq.Width), int(s.ptyReq.Height), s.ptyReq.Term, "honey", channel)
	s.term = terminal.NewTerminal(tLog, "$ ")
	defer channel.Close()

cmdLoop:
	for {
		cmd, err := s.term.ReadLine()
		log.Printf("[%v] typed command %v", s.user, cmd)
		switch {
		case err != nil:
			log.Printf("Err:%v", err)
			break cmdLoop
		case strings.TrimSpace(cmd) == "":
			//Do nothing
		case cmd == "logout", cmd == "quit":
			log.Printf("User [%v] logged out", s.user)
			return
		case strings.HasPrefix(cmd, "ls"):

		default:
			args := strings.SplitN(cmd, " ", 2)
			s.term.Write([]byte(fmt.Sprintf("%v: command not found\n", args[0])))
		}
	}
}
