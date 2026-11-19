package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// EmailSender надсилає листи через SMTP.
type EmailSender struct {
	host     string
	port     int
	username string
	password string
	from     string
	startTLS bool
}

// NewEmailSender створює відправника пошти.
func NewEmailSender(host string, port int, username, password, from string, startTLS bool) *EmailSender {
	return &EmailSender{host: host, port: port, username: username, password: password, from: from, startTLS: startTLS}
}

// Name повертає назву каналу.
func (e *EmailSender) Name() string { return ChannelEmail }

// Send надсилає лист адресату.
func (e *EmailSender) Send(ctx context.Context, target Target, message Message) error {
	if target.Email == "" {
		return Permanent{Err: errors.New("адреса пошти відсутня")}
	}
	letter := e.compose(target.Email, message)
	address := net.JoinHostPort(e.host, strconv.Itoa(e.port))

	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var connection net.Conn
	var err error
	if e.port == 465 {
		connection, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: e.host, MinVersion: tls.VersionTLS12})
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return Retryable{Err: fmt.Errorf("з'єднання з поштовим сервером: %w", err)}
	}

	client, err := smtp.NewClient(connection, e.host)
	if err != nil {
		connection.Close()
		return Retryable{Err: fmt.Errorf("поштовий сеанс: %w", err)}
	}
	defer client.Close()

	if e.startTLS && e.port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: e.host, MinVersion: tls.VersionTLS12}); err != nil {
				return Retryable{Err: fmt.Errorf("STARTTLS: %w", err)}
			}
		} else {
			return Permanent{Err: errors.New("поштовий сервер не підтримує STARTTLS")}
		}
	}
	if e.username != "" {
		if err := client.Auth(smtp.PlainAuth("", e.username, e.password, e.host)); err != nil {
			return Permanent{Err: fmt.Errorf("автентифікація на поштовому сервері: %w", err)}
		}
	}
	if err := client.Mail(e.from); err != nil {
		return Retryable{Err: fmt.Errorf("відправник листа: %w", err)}
	}
	if err := client.Rcpt(target.Email); err != nil {
		return Permanent{Err: fmt.Errorf("адресат листа: %w", err)}
	}
	writer, err := client.Data()
	if err != nil {
		return Retryable{Err: err}
	}
	if _, err := writer.Write([]byte(letter)); err != nil {
		return Retryable{Err: err}
	}
	if err := writer.Close(); err != nil {
		return Retryable{Err: err}
	}
	return client.Quit()
}

func (e *EmailSender) compose(to string, message Message) string {
	var body strings.Builder
	body.WriteString("From: " + e.from + "\r\n")
	body.WriteString("To: " + to + "\r\n")
	body.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", message.Title) + "\r\n")
	body.WriteString("MIME-Version: 1.0\r\n")
	body.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	body.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	body.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	body.WriteString("\r\n")
	body.WriteString(message.Body)
	if message.URL != "" {
		body.WriteString("\r\n\r\n" + message.URL)
	}
	body.WriteString("\r\n\r\n--\r\nЦе автоматичний лист застосунку «Догляд». Відповідати на нього не потрібно.\r\n")
	return body.String()
}

// LogSender виводить повідомлення у журнал замість зовнішньої доставки.
// Використовується під час локальної розробки, коли SMTP не налаштовано.
type LogSender struct {
	channel string
	log     *slog.Logger
}

// NewLogSender створює журнальний канал із заданою назвою.
func NewLogSender(channel string, log *slog.Logger) *LogSender {
	return &LogSender{channel: channel, log: log}
}

// Name повертає назву каналу.
func (l *LogSender) Name() string { return l.channel }

// Send записує повідомлення у журнал сервера.
func (l *LogSender) Send(_ context.Context, target Target, message Message) error {
	l.log.Info("Повідомлення не надіслано: канал працює в журнальному режимі",
		"канал", l.channel,
		"адресат", firstNonEmpty(target.Email, target.TelegramChatID, target.Push.Endpoint),
		"заголовок", message.Title,
		"текст", message.Body,
		"посилання", message.URL,
	)
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return "невідомо"
}
