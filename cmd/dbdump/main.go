package main

import (
	"fmt"
	"os"

	"github.com/kamrul1157024/helios/internal/store"
)

func main() {
	db, err := store.Open(os.Args[1])
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer db.Close()
	ns, _ := db.ListNotifications("", "", "")
	for _, n := range ns {
		t, d := "", ""
		if n.Title != nil {
			t = *n.Title
		}
		if n.Detail != nil {
			d = *n.Detail
		}
		fmt.Printf("type=%-18s source=%-7s status=%-9s session=%.8s %q / %q\n",
			n.Type, n.Source, n.Status, n.SourceSession, t, d)
	}
	if len(ns) == 0 {
		fmt.Println("(no notifications)")
	}
}
