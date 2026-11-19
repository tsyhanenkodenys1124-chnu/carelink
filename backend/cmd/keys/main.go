// Команда keys створює пару ключів VAPID для Web Push.
// Виведені значення додають до файлу середовища як VAPID_PUBLIC_KEY і VAPID_PRIVATE_KEY.
package main

import (
	"fmt"
	"os"

	"doglyad/backend/internal/notify"
)

func main() {
	publicKey, privateKey, err := notify.GenerateVAPIDKeys()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Не вдалося створити ключі:", err)
		os.Exit(1)
	}
	fmt.Println("# Ключі Web Push. Приватний ключ зберігайте лише на сервері.")
	fmt.Println("VAPID_PUBLIC_KEY=" + publicKey)
	fmt.Println("VAPID_PRIVATE_KEY=" + privateKey)
	fmt.Println("VAPID_SUBJECT=mailto:admin@example.org")
}
