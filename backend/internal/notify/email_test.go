package notify

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP — мінімальний поштовий сервер для перевірки відправника.
// Він говорить рівно стільки, скільки потрібно для одного листа без TLS.
type fakeSMTP struct {
	listener net.Listener
	mu       sync.Mutex
	letters  []string
	from     string
	to       string
	done     chan struct{}
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("тестовий поштовий сервер: %v", err)
	}
	server := &fakeSMTP{listener: listener, done: make(chan struct{})}
	go server.serve()
	t.Cleanup(func() {
		listener.Close()
	})
	return server
}

func (f *fakeSMTP) address() (string, int) {
	addr := f.listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func (f *fakeSMTP) serve() {
	connection, err := f.listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	defer close(f.done)

	reader := bufio.NewReader(connection)
	write := func(line string) { _, _ = connection.Write([]byte(line + "\r\n")) }

	write("220 test.local ESMTP")
	var body strings.Builder
	inData := false
	for {
		_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		if inData {
			if line == "." {
				inData = false
				f.mu.Lock()
				f.letters = append(f.letters, body.String())
				f.mu.Unlock()
				write("250 Прийнято")
				continue
			}
			body.WriteString(line + "\n")
			continue
		}

		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			// Без STARTTLS у списку: відправник має обійтися без шифрування.
			write("250-test.local")
			write("250 SIZE 10240000")
		case strings.HasPrefix(upper, "MAIL FROM"):
			f.mu.Lock()
			f.from = line
			f.mu.Unlock()
			write("250 OK")
		case strings.HasPrefix(upper, "RCPT TO"):
			f.mu.Lock()
			f.to = line
			f.mu.Unlock()
			write("250 OK")
		case upper == "DATA":
			inData = true
			write("354 Надсилайте")
		case upper == "QUIT":
			write("221 Бувайте")
			return
		default:
			write("250 OK")
		}
	}
}

func (f *fakeSMTP) letter(t *testing.T) string {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("поштовий сервер не отримав листа")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.letters) == 0 {
		t.Fatal("листів не надійшло")
	}
	return f.letters[0]
}

func TestEmailSenderDeliversLetter(t *testing.T) {
	server := startFakeSMTP(t)
	host, port := server.address()
	// STARTTLS вимкнено: тестовий сервер працює відкритим каналом.
	sender := NewEmailSender(host, port, "", "", "doglyad@example.org", false)

	err := sender.Send(context.Background(), Target{Email: "olena@example.org"}, Message{
		Kind:  KindHelp,
		Title: "Потрібна допомога: Маріє Іванівно",
		Body:  "Підопічний натиснув кнопку «Потрібна допомога».",
		URL:   "https://care.example.org/?tab=signals",
	})
	if err != nil {
		t.Fatalf("відправлення листа: %v", err)
	}

	letter := server.letter(t)
	for _, want := range []string{
		"To: olena@example.org",
		"From: doglyad@example.org",
		"Content-Type: text/plain; charset=utf-8",
		"Підопічний натиснув кнопку",
		"https://care.example.org/?tab=signals",
	} {
		if !strings.Contains(letter, want) {
			t.Fatalf("у листі немає %q:\n%s", want, letter)
		}
	}
	// Заголовок містить кирилицю, тому має бути закодований за RFC 2047.
	if !strings.Contains(letter, "Subject: =?utf-8?") {
		t.Fatalf("тему листа не закодовано:\n%s", letter)
	}
	if !strings.Contains(server.from, "doglyad@example.org") {
		t.Fatalf("неправильний відправник у конверті: %q", server.from)
	}
	if !strings.Contains(server.to, "olena@example.org") {
		t.Fatalf("неправильний адресат у конверті: %q", server.to)
	}
}

func TestEmailSenderRequiresStartTLSWhenAsked(t *testing.T) {
	server := startFakeSMTP(t)
	host, port := server.address()
	// Сервер не оголошує STARTTLS, тому відправник має відмовитися надсилати.
	sender := NewEmailSender(host, port, "", "", "doglyad@example.org", true)

	err := sender.Send(context.Background(), Target{Email: "olena@example.org"}, Message{Title: "Перевірка"})
	if err == nil {
		t.Fatal("лист надіслано відкритим каналом, хоча вимагався STARTTLS")
	}
	if !IsPermanent(err) {
		t.Fatalf("очікувалася стала помилка, отримано %v", err)
	}
}

func TestEmailSenderRejectsEmptyAddress(t *testing.T) {
	sender := NewEmailSender("127.0.0.1", 25, "", "", "doglyad@example.org", false)
	err := sender.Send(context.Background(), Target{}, Message{Title: "Перевірка"})
	if !IsPermanent(err) {
		t.Fatalf("порожня адреса має давати сталу помилку, отримано %v", err)
	}
}
