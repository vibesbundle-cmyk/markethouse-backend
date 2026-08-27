package main

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	h, err := bcrypt.GenerateFromPassword([]byte("Testpass123!"), bcrypt.DefaultCost)
	if err != nil { panic(err) }
	fmt.Println(string(h))
}
