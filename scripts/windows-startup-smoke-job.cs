using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;

namespace KarteStartupSmoke
{
    public sealed class ProcessJob : IDisposable
    {
        private const uint CREATE_SUSPENDED = 0x00000004;
        private const uint CREATE_UNICODE_ENVIRONMENT = 0x00000400;
        private const uint STARTF_USESTDHANDLES = 0x00000100;
        private const uint JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x00002000;
        private const uint GENERIC_READ = 0x80000000;
        private const uint GENERIC_WRITE = 0x40000000;
        private const uint FILE_SHARE_READ = 0x00000001;
        private const uint FILE_SHARE_WRITE = 0x00000002;
        private const uint FILE_SHARE_DELETE = 0x00000004;
        private const uint CREATE_ALWAYS = 2;
        private const uint OPEN_EXISTING = 3;
        private const uint FILE_ATTRIBUTE_NORMAL = 0x00000080;
        private const uint WAIT_OBJECT_0 = 0;
        private const uint WAIT_TIMEOUT = 258;
        private const uint INFINITE = 0xffffffff;
        private const int JobObjectBasicAccountingInformation = 1;
        private const int JobObjectExtendedLimitInformation = 9;

        private IntPtr jobHandle;
        private IntPtr processHandle;
        private bool disposed;

        private ProcessJob(IntPtr jobHandle, IntPtr processHandle, uint processId)
        {
            this.jobHandle = jobHandle;
            this.processHandle = processHandle;
            ProcessId = checked((int)processId);
        }

        public int ProcessId { get; private set; }

        public bool HasExited
        {
            get
            {
                EnsureNotDisposed();
                uint result = NativeMethods.WaitForSingleObject(processHandle, 0);
                if (result == WAIT_OBJECT_0)
                {
                    return true;
                }
                if (result == WAIT_TIMEOUT)
                {
                    return false;
                }
                throw LastWin32Exception("inspect startup smoke process");
            }
        }

        public int ExitCode
        {
            get
            {
                EnsureNotDisposed();
                uint exitCode;
                if (!NativeMethods.GetExitCodeProcess(processHandle, out exitCode))
                {
                    throw LastWin32Exception("read startup smoke process exit code");
                }
                return unchecked((int)exitCode);
            }
        }

        public uint ActiveProcessCount
        {
            get
            {
                EnsureNotDisposed();
                JOBOBJECT_BASIC_ACCOUNTING_INFORMATION accounting;
                uint returnedLength;
                if (!NativeMethods.QueryInformationJobObject(
                    jobHandle,
                    JobObjectBasicAccountingInformation,
                    out accounting,
                    (uint)Marshal.SizeOf(typeof(JOBOBJECT_BASIC_ACCOUNTING_INFORMATION)),
                    out returnedLength))
                {
                    throw LastWin32Exception("inspect startup smoke job");
                }
                return accounting.ActiveProcesses;
            }
        }

        public static ProcessJob Start(
            string applicationPath,
            string workingDirectory,
            string stdoutPath,
            string stderrPath)
        {
            RequireAbsoluteFile(applicationPath, "applicationPath");
            RequireAbsoluteDirectory(workingDirectory, "workingDirectory");
            RequireAbsolutePath(stdoutPath, "stdoutPath");
            RequireAbsolutePath(stderrPath, "stderrPath");
            if (applicationPath.IndexOf('"') >= 0)
            {
                throw new ArgumentException("applicationPath contains an invalid quote", "applicationPath");
            }

            IntPtr job = IntPtr.Zero;
            IntPtr stdout = new IntPtr(-1);
            IntPtr stderr = new IntPtr(-1);
            IntPtr stdin = new IntPtr(-1);
            PROCESS_INFORMATION processInformation = new PROCESS_INFORMATION();
            bool processCreated = false;
            try
            {
                job = NativeMethods.CreateJobObject(IntPtr.Zero, null);
                if (job == IntPtr.Zero)
                {
                    throw LastWin32Exception("create startup smoke job");
                }
                ConfigureJobToKillOnClose(job);

                SECURITY_ATTRIBUTES security = new SECURITY_ATTRIBUTES();
                security.nLength = Marshal.SizeOf(typeof(SECURITY_ATTRIBUTES));
                security.bInheritHandle = true;
                stdout = NativeMethods.CreateFile(
                    stdoutPath,
                    GENERIC_WRITE,
                    FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                    ref security,
                    CREATE_ALWAYS,
                    FILE_ATTRIBUTE_NORMAL,
                    IntPtr.Zero);
                ThrowIfInvalidHandle(stdout, "open startup smoke stdout");
                stderr = NativeMethods.CreateFile(
                    stderrPath,
                    GENERIC_WRITE,
                    FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                    ref security,
                    CREATE_ALWAYS,
                    FILE_ATTRIBUTE_NORMAL,
                    IntPtr.Zero);
                ThrowIfInvalidHandle(stderr, "open startup smoke stderr");
                stdin = NativeMethods.CreateFile(
                    "NUL",
                    GENERIC_READ,
                    FILE_SHARE_READ | FILE_SHARE_WRITE,
                    ref security,
                    OPEN_EXISTING,
                    FILE_ATTRIBUTE_NORMAL,
                    IntPtr.Zero);
                ThrowIfInvalidHandle(stdin, "open startup smoke stdin");

                STARTUPINFO startupInfo = new STARTUPINFO();
                startupInfo.cb = Marshal.SizeOf(typeof(STARTUPINFO));
                startupInfo.dwFlags = STARTF_USESTDHANDLES;
                startupInfo.hStdInput = stdin;
                startupInfo.hStdOutput = stdout;
                startupInfo.hStdError = stderr;
                StringBuilder commandLine = new StringBuilder("\"" + applicationPath + "\"");
                if (!NativeMethods.CreateProcess(
                    applicationPath,
                    commandLine,
                    IntPtr.Zero,
                    IntPtr.Zero,
                    true,
                    CREATE_SUSPENDED | CREATE_UNICODE_ENVIRONMENT,
                    IntPtr.Zero,
                    workingDirectory,
                    ref startupInfo,
                    out processInformation))
                {
                    throw LastWin32Exception("create suspended startup smoke process");
                }
                processCreated = true;
                if (!NativeMethods.AssignProcessToJobObject(job, processInformation.hProcess))
                {
                    throw LastWin32Exception("assign startup smoke process to job");
                }
                if (NativeMethods.ResumeThread(processInformation.hThread) == uint.MaxValue)
                {
                    throw LastWin32Exception("resume startup smoke process");
                }

                ProcessJob result = new ProcessJob(job, processInformation.hProcess, processInformation.dwProcessId);
                job = IntPtr.Zero;
                processInformation.hProcess = IntPtr.Zero;
                return result;
            }
            catch
            {
                if (processCreated && processInformation.hProcess != IntPtr.Zero)
                {
                    NativeMethods.TerminateProcess(processInformation.hProcess, 1);
                    NativeMethods.WaitForSingleObject(processInformation.hProcess, 5000);
                }
                throw;
            }
            finally
            {
                CloseIfValid(processInformation.hThread);
                CloseIfValid(processInformation.hProcess);
                CloseIfValid(stdin);
                CloseIfValid(stderr);
                CloseIfValid(stdout);
                CloseIfValid(job);
            }
        }

        public void WaitForExit()
        {
            EnsureNotDisposed();
            if (NativeMethods.WaitForSingleObject(processHandle, INFINITE) != WAIT_OBJECT_0)
            {
                throw LastWin32Exception("wait for startup smoke process");
            }
        }

        public bool WaitForEmpty(int timeoutMilliseconds)
        {
            EnsureNotDisposed();
            if (timeoutMilliseconds < 0)
            {
                throw new ArgumentOutOfRangeException("timeoutMilliseconds");
            }
            Stopwatch timer = Stopwatch.StartNew();
            do
            {
                if (ActiveProcessCount == 0)
                {
                    return true;
                }
                if (timer.ElapsedMilliseconds >= timeoutMilliseconds)
                {
                    return false;
                }
                Thread.Sleep(50);
            }
            while (true);
        }

        public void Terminate(uint exitCode)
        {
            EnsureNotDisposed();
            if (!NativeMethods.TerminateJobObject(jobHandle, exitCode))
            {
                throw LastWin32Exception("terminate startup smoke job");
            }
        }

        public void Dispose()
        {
            if (disposed)
            {
                return;
            }
            disposed = true;
            CloseIfValid(jobHandle);
            jobHandle = IntPtr.Zero;
            CloseIfValid(processHandle);
            processHandle = IntPtr.Zero;
        }

        private static void ConfigureJobToKillOnClose(IntPtr job)
        {
            JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits = new JOBOBJECT_EXTENDED_LIMIT_INFORMATION();
            limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
            int size = Marshal.SizeOf(typeof(JOBOBJECT_EXTENDED_LIMIT_INFORMATION));
            IntPtr buffer = Marshal.AllocHGlobal(size);
            try
            {
                Marshal.StructureToPtr(limits, buffer, false);
                if (!NativeMethods.SetInformationJobObject(
                    job,
                    JobObjectExtendedLimitInformation,
                    buffer,
                    (uint)size))
                {
                    throw LastWin32Exception("configure startup smoke job");
                }
            }
            finally
            {
                Marshal.FreeHGlobal(buffer);
            }
        }

        private static void RequireAbsoluteFile(string path, string parameterName)
        {
            RequireAbsolutePath(path, parameterName);
            if (!File.Exists(path))
            {
                throw new FileNotFoundException(parameterName + " does not exist", path);
            }
        }

        private static void RequireAbsoluteDirectory(string path, string parameterName)
        {
            RequireAbsolutePath(path, parameterName);
            if (!Directory.Exists(path))
            {
                throw new DirectoryNotFoundException(parameterName + " does not exist: " + path);
            }
        }

        private static void RequireAbsolutePath(string path, string parameterName)
        {
            if (String.IsNullOrWhiteSpace(path) || !Path.IsPathRooted(path) || Path.GetFullPath(path) != path)
            {
                throw new ArgumentException(parameterName + " must be an absolute clean path", parameterName);
            }
        }

        private static void ThrowIfInvalidHandle(IntPtr handle, string operation)
        {
            if (handle == IntPtr.Zero || handle == new IntPtr(-1))
            {
                throw LastWin32Exception(operation);
            }
        }

        private static Win32Exception LastWin32Exception(string operation)
        {
            return new Win32Exception(Marshal.GetLastWin32Error(), operation);
        }

        private static void CloseIfValid(IntPtr handle)
        {
            if (handle != IntPtr.Zero && handle != new IntPtr(-1))
            {
                NativeMethods.CloseHandle(handle);
            }
        }

        private void EnsureNotDisposed()
        {
            if (disposed)
            {
                throw new ObjectDisposedException("ProcessJob");
            }
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct SECURITY_ATTRIBUTES
        {
            public int nLength;
            public IntPtr lpSecurityDescriptor;
            [MarshalAs(UnmanagedType.Bool)]
            public bool bInheritHandle;
        }

        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        private struct STARTUPINFO
        {
            public int cb;
            public string lpReserved;
            public string lpDesktop;
            public string lpTitle;
            public uint dwX;
            public uint dwY;
            public uint dwXSize;
            public uint dwYSize;
            public uint dwXCountChars;
            public uint dwYCountChars;
            public uint dwFillAttribute;
            public uint dwFlags;
            public short wShowWindow;
            public short cbReserved2;
            public IntPtr lpReserved2;
            public IntPtr hStdInput;
            public IntPtr hStdOutput;
            public IntPtr hStdError;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct PROCESS_INFORMATION
        {
            public IntPtr hProcess;
            public IntPtr hThread;
            public uint dwProcessId;
            public uint dwThreadId;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct IO_COUNTERS
        {
            public ulong ReadOperationCount;
            public ulong WriteOperationCount;
            public ulong OtherOperationCount;
            public ulong ReadTransferCount;
            public ulong WriteTransferCount;
            public ulong OtherTransferCount;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct JOBOBJECT_BASIC_LIMIT_INFORMATION
        {
            public long PerProcessUserTimeLimit;
            public long PerJobUserTimeLimit;
            public uint LimitFlags;
            public UIntPtr MinimumWorkingSetSize;
            public UIntPtr MaximumWorkingSetSize;
            public uint ActiveProcessLimit;
            public UIntPtr Affinity;
            public uint PriorityClass;
            public uint SchedulingClass;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct JOBOBJECT_EXTENDED_LIMIT_INFORMATION
        {
            public JOBOBJECT_BASIC_LIMIT_INFORMATION BasicLimitInformation;
            public IO_COUNTERS IoInfo;
            public UIntPtr ProcessMemoryLimit;
            public UIntPtr JobMemoryLimit;
            public UIntPtr PeakProcessMemoryUsed;
            public UIntPtr PeakJobMemoryUsed;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct JOBOBJECT_BASIC_ACCOUNTING_INFORMATION
        {
            public long TotalUserTime;
            public long TotalKernelTime;
            public long ThisPeriodTotalUserTime;
            public long ThisPeriodTotalKernelTime;
            public uint TotalPageFaultCount;
            public uint TotalProcesses;
            public uint ActiveProcesses;
            public uint TotalTerminatedProcesses;
        }

        private static class NativeMethods
        {
            [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
            internal static extern IntPtr CreateJobObject(IntPtr jobAttributes, string name);

            [DllImport("kernel32.dll", SetLastError = true)]
            [return: MarshalAs(UnmanagedType.Bool)]
            internal static extern bool SetInformationJobObject(
                IntPtr job,
                int informationClass,
                IntPtr information,
                uint informationLength);

            [DllImport("kernel32.dll", SetLastError = true)]
            [return: MarshalAs(UnmanagedType.Bool)]
            internal static extern bool QueryInformationJobObject(
                IntPtr job,
                int informationClass,
                out JOBOBJECT_BASIC_ACCOUNTING_INFORMATION information,
                uint informationLength,
                out uint returnLength);

            [DllImport("kernel32.dll", SetLastError = true)]
            [return: MarshalAs(UnmanagedType.Bool)]
            internal static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

            [DllImport("kernel32.dll", SetLastError = true)]
            [return: MarshalAs(UnmanagedType.Bool)]
            internal static extern bool TerminateJobObject(IntPtr job, uint exitCode);

            [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
            internal static extern IntPtr CreateFile(
                string fileName,
                uint desiredAccess,
                uint shareMode,
                ref SECURITY_ATTRIBUTES securityAttributes,
                uint creationDisposition,
                uint flagsAndAttributes,
                IntPtr templateFile);

            [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
            [return: MarshalAs(UnmanagedType.Bool)]
            internal static extern bool CreateProcess(
                string applicationName,
                StringBuilder commandLine,
                IntPtr processAttributes,
                IntPtr threadAttributes,
                [MarshalAs(UnmanagedType.Bool)] bool inheritHandles,
                uint creationFlags,
                IntPtr environment,
                string currentDirectory,
                ref STARTUPINFO startupInfo,
                out PROCESS_INFORMATION processInformation);

            [DllImport("kernel32.dll", SetLastError = true)]
            internal static extern uint ResumeThread(IntPtr thread);

            [DllImport("kernel32.dll", SetLastError = true)]
            [return: MarshalAs(UnmanagedType.Bool)]
            internal static extern bool GetExitCodeProcess(IntPtr process, out uint exitCode);

            [DllImport("kernel32.dll", SetLastError = true)]
            [return: MarshalAs(UnmanagedType.Bool)]
            internal static extern bool TerminateProcess(IntPtr process, uint exitCode);

            [DllImport("kernel32.dll", SetLastError = true)]
            internal static extern uint WaitForSingleObject(IntPtr handle, uint milliseconds);

            [DllImport("kernel32.dll", SetLastError = true)]
            [return: MarshalAs(UnmanagedType.Bool)]
            internal static extern bool CloseHandle(IntPtr handle);
        }
    }
}
