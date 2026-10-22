package desktop

import "golang.org/x/sys/windows"

// ShowError shows an error that stops the program. Started by a double-click there is no console to see it in.
func ShowError(msg string) {
	text, _ := windows.UTF16PtrFromString(msg)
	title, _ := windows.UTF16PtrFromString(Name)
	windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
}
