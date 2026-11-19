$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT') { throw 'This module requires Windows DPAPI.' }

if (-not ('KarteSynthetic.ProcessJob' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;
namespace KarteSynthetic {
    public sealed class ProcessJob : IDisposable {
        [StructLayout(LayoutKind.Sequential)] struct BasicLimits {
            public long ProcessTime, JobTime;
            public uint Flags;
            public UIntPtr MinWorkingSet, MaxWorkingSet;
            public uint ActiveProcesses;
            public UIntPtr Affinity;
            public uint Priority, Scheduling;
        }
        [StructLayout(LayoutKind.Sequential)] struct IoCounters {
            public ulong ReadOperations, WriteOperations, OtherOperations;
            public ulong ReadBytes, WriteBytes, OtherBytes;
        }
        [StructLayout(LayoutKind.Sequential)] struct ExtendedLimits {
            public BasicLimits Basic;
            public IoCounters Io;
            public UIntPtr ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory;
        }
        [DllImport("kernel32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
        static extern SafeFileHandle CreateJobObject(IntPtr attributes, string name);
        [DllImport("kernel32.dll", SetLastError=true)]
        static extern bool SetInformationJobObject(SafeFileHandle job, int kind, ref ExtendedLimits info, uint length);
        [DllImport("kernel32.dll", SetLastError=true)]
        static extern bool AssignProcessToJobObject(SafeFileHandle job, IntPtr process);
        readonly SafeFileHandle handle;
        public ProcessJob() {
            handle=CreateJobObject(IntPtr.Zero, null);
            if(handle.IsInvalid) throw new Win32Exception(Marshal.GetLastWin32Error());
            var limits=new ExtendedLimits();
            limits.Basic.Flags=0x2000; // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
            if(!SetInformationJobObject(handle, 9, ref limits, (uint)Marshal.SizeOf(limits))) {
                int error=Marshal.GetLastWin32Error(); handle.Dispose(); throw new Win32Exception(error);
            }
        }
        public void Assign(Process process) {
            if(!AssignProcessToJobObject(handle,process.Handle)) throw new Win32Exception(Marshal.GetLastWin32Error());
        }
        public void Dispose() { handle.Dispose(); }
    }
}
'@
}
function New-KarteProcessJob { return [KarteSynthetic.ProcessJob]::new() }

function Read-KarteSavedKey([string]$Path) {
    try { $key = Import-Clixml -LiteralPath $Path -ErrorAction Stop } catch {
        throw 'Saved key could not be decrypted. Run this launcher with -SetupAutostart -ReplaceKey in your Windows account.'
    }
    if ($key -isnot [Security.SecureString] -or $key.Length -eq 0) {
        if ($key -is [IDisposable]) { $key.Dispose() }
        throw 'Saved key is invalid. Run this launcher with -SetupAutostart -ReplaceKey.'
    }
    return $key
}

function Save-KarteKey([Security.SecureString]$Key, [string]$Path) {
    if (-not $Key -or $Key.Length -eq 0) { throw 'An empty key cannot be saved.' }
    $parent = Split-Path -Parent $Path
    [void][IO.Directory]::CreateDirectory($parent)
    $temporary = Join-Path $parent ([IO.Path]::GetRandomFileName())
    $check = $null
    try {
        $Key | Export-Clixml -LiteralPath $temporary -Encoding UTF8
        $check = Read-KarteSavedKey $temporary
        if ($check.Length -ne $Key.Length) { throw 'Saved credential verification failed.' }
        if (Test-Path -LiteralPath $Path) { [IO.File]::Replace($temporary, $Path, [NullString]::Value) }
        else { [IO.File]::Move($temporary, $Path) }
    } finally {
        if ($check) { $check.Dispose() }
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary }
    }
}

function ConvertTo-KarteCloudflareToken([Security.SecureString]$InputValue) {
    $buffer = [IntPtr]::Zero
    try {
        $buffer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($InputValue)
        $plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($buffer).Trim()
        $match = [regex]::Match($plain, '\A(?:[^\r\n]+[ \t])?(?<token>eyJ[A-Za-z0-9_+/=-]+)\z')
        if (-not $match.Success) { throw 'Expected the eyJ... Cloudflare token or its one-line dashboard command.' }
        return ConvertTo-SecureString -String $match.Groups['token'].Value -AsPlainText -Force
    } finally {
        $plain = $null
        $match = $null
        if ($buffer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($buffer) }
    }
}

function Register-KarteAutostart(
    [string]$ScriptPath, [string]$Id, [string]$Name,
    [switch]$Grok, [switch]$Paused
) {
    if ($ScriptPath.Contains('"')) { throw 'Unsupported launcher path.' }
    if (-not $Grok -and $Id -notmatch '^tunnel_[A-Za-z0-9_-]+$') { throw 'Invalid Tunnel ID.' }
    $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    $executable = Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe'
    $suffix = if ($Grok) { '-NamedTunnel' } else { '-TunnelId "' + $Id + '"' }
    $role = if ($Grok) { 'Grok' } else { 'ChatGPT' }
    $description = "Karte synthetic $role MCP logon startup (current user only)."
    $legacyArguments = '-NoProfile -NonInteractive -WindowStyle Hidden -File "{0}" -NonInteractive {1}' -f $ScriptPath, $suffix
    # Apply only to this trusted launcher process; keep the user's policy intact.
    $arguments = '-NoProfile -NonInteractive -ExecutionPolicy RemoteSigned -WindowStyle Hidden -File "{0}" -NonInteractive {1}' -f $ScriptPath, $suffix
    $existing = Get-ScheduledTask -TaskPath '\' -TaskName $Name -ErrorAction SilentlyContinue
    if ($existing) {
        $actions = @($existing.Actions)
        if ($existing.Description -ne $description -or $actions.Count -ne 1 -or
            $actions[0].Execute -ne $executable -or $actions[0].Arguments -notin @($arguments, $legacyArguments)) {
            throw 'A different scheduled task uses this name; it has not been changed.'
        }
    }
    $action = New-ScheduledTaskAction -Execute $executable -Argument $arguments -WorkingDirectory (Split-Path -Parent $ScriptPath)
    $logon = New-ScheduledTaskTrigger -AtLogOn -User $user
    $logon.Delay = 'PT20S'
    # An indefinite one-minute trigger also recovers after long network outages,
    # a clean child exit, or exhaustion of the scheduler's failure retry window.
    $retry = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(1) -RepetitionInterval (New-TimeSpan -Minutes 1)
    $principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 10 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
    if ($Paused) { $settings.Enabled = $false }
    Register-ScheduledTask -TaskPath '\' -TaskName $Name -Action $action -Trigger @($logon, $retry) -Principal $principal -Settings $settings -Description $description -Force | Out-Null
}

function Get-KarteRuntimePorts([string]$ScriptRoot) {
    $httpPort=8791; $metricsPort=8792
    $file=Join-Path $ScriptRoot 'private-startup/runtime-ports.json'
    if(Test-Path -LiteralPath $file){
        if((Get-Item -LiteralPath $file).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Unsafe runtime port configuration.'}
        $config=[IO.File]::ReadAllText($file)|ConvertFrom-Json
        if(@($config.PSObject.Properties).Count -ne 2 -or -not $config.PSObject.Properties['GrokHTTP'] -or -not $config.PSObject.Properties['GrokMetrics']){throw 'Invalid runtime port configuration.'}
        foreach($p in @($config.GrokHTTP,$config.GrokMetrics)){if($p -isnot [int] -and $p -isnot [long] -or $p -lt 1024 -or $p -gt 65535 -or $p -eq 18640){throw 'Invalid runtime port configuration.'}}
        $httpPort=[int]$config.GrokHTTP; $metricsPort=[int]$config.GrokMetrics
        if($httpPort -eq $metricsPort){throw 'Runtime ports must be distinct.'}
    }
    return [pscustomobject]@{HTTP=$httpPort;Metrics=$metricsPort;Origin=('http://127.0.0.1:'+ $httpPort)}
}

function Get-KarteStartupStatus([string]$Name, [string]$KeyFile) {
    $task = Get-ScheduledTask -TaskPath '\' -TaskName $Name -ErrorAction SilentlyContinue
    $info = if ($task) { Get-ScheduledTaskInfo -TaskPath '\' -TaskName $Name }
    [pscustomobject]@{
        TaskName = $Name
        SavedKeyPresent = Test-Path -LiteralPath $KeyFile
        TaskState = if ($task) { [string]$task.State } else { 'NotRegistered' }
        Enabled = if ($task) { $task.Settings.Enabled } else { $false }
        LastTaskResult = if ($info) { $info.LastTaskResult } else { $null }
    }
}

function Test-KarteListenerOwner([int]$Port, [int]$ProcessId) {
    $listeners = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
    return @($listeners | Where-Object { $_.OwningProcess -eq $ProcessId -and $_.LocalAddress -eq '127.0.0.1' }).Count -eq 1
}

function Test-KarteGrokOwnership([string]$ScriptRoot, [object]$Ports) {
    try {
        $record = [IO.File]::ReadAllText((Join-Path $ScriptRoot 'synthetic-grok-runtime.json')) | ConvertFrom-Json
        $server = Get-Process -Id ([int]$record.ServerId) -ErrorAction Stop
        $tunnel = Get-Process -Id ([int]$record.TunnelId) -ErrorAction Stop
        $karte = Join-Path $ScriptRoot 'karte-mcp-ea4b9b2.exe'
        if (-not (Test-Path -LiteralPath $karte)) { $karte = Join-Path $ScriptRoot 'karte-mcp.exe' }
        $expectedTunnel = Join-Path $ScriptRoot 'openai-tunnel-client-v0.0.21/cloudflared.exe'
        return $server.Path -ieq $karte -and $tunnel.Path -ieq $expectedTunnel -and
            [string]$server.StartTime.ToUniversalTime().Ticks -ceq [string]$record.ServerStartTicks -and
            [string]$tunnel.StartTime.ToUniversalTime().Ticks -ceq [string]$record.TunnelStartTicks -and
            (Test-KarteListenerOwner $Ports.HTTP $server.Id) -and (Test-KarteListenerOwner $Ports.Metrics $tunnel.Id)
    } catch { return $false }
}

Export-ModuleMember -Function Read-KarteSavedKey, Save-KarteKey, ConvertTo-KarteCloudflareToken, Register-KarteAutostart, Get-KarteStartupStatus, New-KarteProcessJob, Get-KarteRuntimePorts, Test-KarteListenerOwner, Test-KarteGrokOwnership
