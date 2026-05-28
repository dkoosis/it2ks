package main

import (
	"fmt"

	"github.com/tmc/it2/client"
	pb "github.com/tmc/it2/proto"
)

func main() {
	_ = client.New
	_ = pb.NotificationType_NOTIFY_ON_KEYSTROKE
	fmt.Println("it2ks deps OK")
}
