package main
import (
	"fmt"
	"goblast/internal/auth"
)
func main() {
	hash := "$argon2id$v=19$m=65536,t=1,p=4$xvbb0MogKspsjSDODFrKHg$nbyhBmFyI5+dwRn7C1ZcMwr+uT5eyIYLNnfHYcc7x/U"
	match, err := auth.VerifyPassword("Feegg12345@", hash)
	fmt.Printf("Match: %v, Err: %v\n", match, err)
}
