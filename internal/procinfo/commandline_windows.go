//go:build windows

package procinfo

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// commandLine reads the process argv out of the process's own memory.
//
// Windows keeps a process's command line in that process's address space
// rather than anywhere a sibling can read it from a file, so the answer
// takes three reads: the PEB address from the kernel, the parameter block
// address from the PEB, and the string itself from the parameter block.
// The alternative - shelling out to WMI for Win32_Process.CommandLine -
// answers the same question, and does it by starting PowerShell and
// waiting a second or more inside the startup of every Claude Code session
// on the machine.
//
// Everything here can fail for ordinary reasons: the session may be gone,
// or running as another user, or elevated. Every one of them comes back as
// an error, which Enabled turns into "unconfirmed" - never into a no.
func commandLine(pid int) ([]string, error) {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return nil, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	var info windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation,
		unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return nil, fmt.Errorf("read the process block of %d: %w", pid, err)
	}
	if info.PebBaseAddress == nil {
		return nil, fmt.Errorf("process %d has no process block", pid)
	}

	// Only the one pointer out of the PEB is read, and only the one string
	// out of the parameter block. Both structures have grown over the
	// years, and a read sized to this build's idea of them would fail
	// against a Windows that disagrees about their tail.
	params, err := readPointer(h, uintptr(unsafe.Pointer(info.PebBaseAddress))+
		unsafe.Offsetof(windows.PEB{}.ProcessParameters))
	if err != nil {
		return nil, fmt.Errorf("read the process block of %d: %w", pid, err)
	}
	var cmd windows.NTUnicodeString
	if err := readRemote(h, params+unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.CommandLine),
		unsafe.Pointer(&cmd), unsafe.Sizeof(cmd)); err != nil {
		return nil, fmt.Errorf("read the parameters of %d: %w", pid, err)
	}
	if cmd.Length == 0 || cmd.Buffer == nil {
		return nil, fmt.Errorf("process %d reports no command line", pid)
	}

	// Length is in bytes and always even, so the buffer is Length/2 UTF-16
	// code units, without a terminator.
	buf := make([]uint16, cmd.Length/2)
	if err := readRemote(h, uintptr(unsafe.Pointer(cmd.Buffer)),
		unsafe.Pointer(&buf[0]), uintptr(cmd.Length)); err != nil {
		return nil, fmt.Errorf("read the command line of %d: %w", pid, err)
	}
	return windows.DecomposeCommandLine(windows.UTF16ToString(buf))
}

// readPointer reads one pointer-sized value out of another process.
func readPointer(h windows.Handle, addr uintptr) (uintptr, error) {
	var p uintptr
	if err := readRemote(h, addr, unsafe.Pointer(&p), unsafe.Sizeof(p)); err != nil {
		return 0, err
	}
	if p == 0 {
		return 0, errors.New("null pointer")
	}
	return p, nil
}

// readRemote fills size bytes at dst from the other process's memory. A
// partial read is refused: half a pointer is not a smaller answer, it is a
// wrong one.
func readRemote(h windows.Handle, addr uintptr, dst unsafe.Pointer, size uintptr) error {
	var read uintptr
	if err := windows.ReadProcessMemory(h, addr, (*byte)(dst), size, &read); err != nil {
		return err
	}
	if read != size {
		return fmt.Errorf("read %d of %d bytes", read, size)
	}
	return nil
}
