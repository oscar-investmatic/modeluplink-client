package service

import (
	"bytes"
	encodingxml "encoding/xml"
	"fmt"
)

func taskEscape(value string) string {
	var b bytes.Buffer
	_ = encodingxml.EscapeText(&b, []byte(value))
	return b.String()
}
func windowsTaskXML(sid, executable, arguments string, startup bool) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
<Triggers><LogonTrigger><Enabled>%t</Enabled><UserId>%s</UserId></LogonTrigger></Triggers>
<Principals><Principal id="Owner"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
<Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><StartWhenAvailable>true</StartWhenAvailable><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><RestartOnFailure><Interval>PT1M</Interval><Count>10</Count></RestartOnFailure></Settings>
<Actions Context="Owner"><Exec><Command>%s</Command><Arguments>%s</Arguments></Exec></Actions></Task>`, startup, taskEscape(sid), taskEscape(sid), taskEscape(executable), taskEscape(arguments))
}
