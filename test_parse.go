package main
import (
	"fmt"
	"github.com/nyaruka/phonenumbers"
)
func main() {
	num, _ := phonenumbers.Parse("+81234567891", "")
	fmt.Println("Japan valid?", phonenumbers.IsValidNumber(num))
	num2, _ := phonenumbers.Parse("+491779589931", "")
	fmt.Println("German valid?", phonenumbers.IsValidNumber(num2))
}
