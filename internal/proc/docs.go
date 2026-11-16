package proc

import "strings"

// docExts are the document types an office suite holds open.
var docExts = map[string]bool{
	".pptx": true, ".ppt": true, ".odp": true, ".docx": true, ".doc": true, ".odt": true,
	".xlsx": true, ".xls": true, ".ods": true, ".rtf": true, ".odg": true, ".pdf": true,
}

// Office reports whether comm is LibreOffice, which runs every open
// document in one process: killing it closes the user's documents too.
func Office(comm string) bool {
	return strings.HasPrefix(comm, "soffice") || comm == "oosplash" || strings.HasPrefix(comm, "libreoffice")
}
