// DRISHTI Map: diagnose and repair AMR laser maps offline, with an optional AI assistant.
//
// The program is one executable. It starts a web server on 127.0.0.1 (this PC only) and shows it in
// its own window. It reads from robots but never writes to them.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"

	"drishtimap/app"
	"drishtimap/server"
)

//go:embed build/icon.ico
var iconICO []byte

var (
	user32      = syscall.NewLazyDLL("user32.dll")
	procDPI     = user32.NewProc("SetProcessDpiAwarenessContext")
	procLoadImg = user32.NewProc("LoadImageW")
	procSendMsg = user32.NewProc("SendMessageW")
	procMsgBox  = user32.NewProc("MessageBoxW")
)

// msgBox shows a message window (the program has no console). icon: 0x10 error, 0x40 information.
func msgBox(text string, icon uintptr) {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString("DRISHTI Map")
	procMsgBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), icon)
}

// setIcon gives the window and its taskbar button the DRISHTI Map icon.
func setIcon(hwnd unsafe.Pointer, dir string) {
	p := filepath.Join(dir, "icon.ico")
	if os.WriteFile(p, iconICO, 0644) != nil {
		return
	}
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return
	}
	for wparam, size := range map[uintptr]uintptr{0: 16, 1: 48} { // ICON_SMALL, ICON_BIG
		if h, _, _ := procLoadImg.Call(0, uintptr(unsafe.Pointer(name)), 1, size, size, 0x10); h != 0 { // IMAGE_ICON, LR_LOADFROMFILE
			procSendMsg.Call(uintptr(hwnd), 0x80, wparam, h) // WM_SETICON
		}
	}
}

func main() {
	port := flag.Int("port", 0, "fixed port for the local server (default: any free port)")
	noWin := flag.Bool("no-window", false, "run the local server only and print its address (for testing)")
	browser := flag.Bool("browser", false, "open in the default web browser instead of the app window")
	wsFlag := flag.String("workspace", "", "workspace folder to use")
	flag.Parse()
	noBox = *noWin

	// default workspace: a "workspace" folder next to the program if there is one, else Documents\DRISHTI Map
	def := ""
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), "workspace"); dirExists(p) {
			def = p
		}
	}
	if def == "" {
		home, _ := os.UserHomeDir()
		def = filepath.Join(home, "Documents", "DRISHTI Map")
	}
	a, err := app.New(def)
	if err != nil {
		fatal(err)
	}
	if *wsFlag != "" {
		if err := a.UseWorkspaceOnce(*wsFlag); err != nil {
			fatal(err)
		}
	}
	srv, err := server.Start(a, *port)
	if err != nil {
		fatal(err)
	}
	defer a.Shutdown()

	if *noWin || *browser {
		fmt.Println(srv.URL())
		if *browser {
			exec.Command("rundll32", "url.dll,FileProtocolHandler", srv.URL()).Start()
		}
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		return
	}
	cfg, _ := os.UserConfigDir()
	if procDPI.Find() == nil {
		procDPI.Call(^uintptr(3)) // per-monitor DPI aware (v2): sharp text on scaled displays
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus:     true,
		DataPath:      filepath.Join(cfg, "DRISHTI Map", "webview"),
		WindowOptions: webview2.WindowOptions{Title: "DRISHTI Map", Width: 1560, Height: 960, Center: true},
	})
	if w == nil {
		// no WebView2 runtime on this PC: use the default browser, and keep running until this box is closed
		exec.Command("rundll32", "url.dll,FileProtocolHandler", srv.URL()).Start()
		msgBox("DRISHTI Map has opened in your web browser, because the Microsoft Edge WebView2 Runtime is not installed on this PC.\n\nKeep this box open while you work. Press OK to close DRISHTI Map.\n\nTo get the normal app window, install the WebView2 Runtime from Microsoft (it is part of Windows 11).", 0x40)
		return
	}
	defer w.Destroy()
	setIcon(w.Window(), filepath.Join(cfg, "DRISHTI Map"))
	w.Navigate(srv.URL())
	w.Run()
}

var noBox bool // server-only runs report errors on the console, not in a message box

func dirExists(p string) bool { s, err := os.Stat(p); return err == nil && s.IsDir() }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "DRISHTI Map:", err)
	if !noBox {
		msgBox("DRISHTI Map could not start:\n\n"+err.Error(), 0x10)
	}
	os.Exit(1)
}
