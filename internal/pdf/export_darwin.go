//go:build darwin

package pdf

/*
#cgo darwin CFLAGS: -x objective-c -fobjc-arc -fmodules
#cgo darwin LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <time.h>
#include <string.h>

// ログを出力するヘルパー関数（ログファイルに直接書き込む）
static void logPDFExport(const char* logPath, const char* msg) {
    if (logPath == NULL || strlen(logPath) == 0) {
        // フォールバック: NSLogを使用
        NSLog(@"%s", msg);
        return;
    }

    FILE *logFile = fopen(logPath, "a");
    if (logFile == NULL) {
        // フォールバック: NSLogを使用
        NSLog(@"Failed to open log file: %s, message: %s", logPath, msg);
        return;
    }

    // タイムスタンプを生成（RFC3339形式: YYYY-MM-DDTHH:MM:SS+09:00）
    time_t now = time(NULL);
    struct tm *tm_info = localtime(&now);
    char timestamp[64];
    // タイムゾーンオフセットを計算
    long offset = tm_info->tm_gmtoff;
    int hours = (int)(offset / 3600);
    int minutes = (int)((offset % 3600) / 60);
    if (minutes < 0) minutes = -minutes; // 絶対値

    strftime(timestamp, sizeof(timestamp), "%Y-%m-%dT%H:%M:%S", tm_info);

    // 既存のフォーマットに合わせて書き込む: {timestamp} [INFO] {message}\n
    // タイムゾーンオフセットを追加（RFC3339形式: +09:00 または -05:00）
    fprintf(logFile, "%s%+03d:%02d [INFO] %s\n", timestamp, hours, minutes, msg);
    fflush(logFile); // 即座にフラッシュして確実に書き込む
    fclose(logFile);
}

static const char* exportHTMLToPDFMac(const char* htmlC, const char* outPathC, const char* logPathC) {
    @autoreleasepool {
        NSString* html = [NSString stringWithUTF8String:htmlC ? htmlC : ""];
        NSString* outPath = [NSString stringWithUTF8String:outPathC ? outPathC : ""];
        const char* logPath = logPathC ? logPathC : "";
        __block const char* retErr = NULL;

        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Starting PDF export to: %@", outPath] UTF8String]);
        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] HTML length: %lu", (unsigned long)[html length]] UTF8String]);

        // Execute on main thread asynchronously to avoid deadlock if caller is already on main thread
        dispatch_semaphore_t sem = dispatch_semaphore_create(0);
        dispatch_async(dispatch_get_main_queue(), ^{
            @autoreleasepool {
                __block BOOL finished = NO;

                @try {
                    logPDFExport(logPath, "[PDF Export] Initializing NSApplication...");
                    if ([NSApplication sharedApplication] == nil) {
                        [NSApplication sharedApplication];
                    }
                    logPDFExport(logPath, "[PDF Export] Creating WKWebView...");
                    WKWebViewConfiguration* config = [WKWebViewConfiguration new];
                    WKWebView* webview = [[WKWebView alloc] initWithFrame:NSMakeRect(0,0,800,1000) configuration:config];
                    NSWindow* window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0,0,800,1000)
                                                                   styleMask:NSWindowStyleMaskTitled
                                                                     backing:NSBackingStoreBuffered
                                                                       defer:NO];
                    [window setReleasedWhenClosed:NO];
                    [window setOpaque:NO];
                    [window setAlphaValue:0.0];
                    [window setIgnoresMouseEvents:YES];
                    [window setContentView:webview];

                    logPDFExport(logPath, "[PDF Export] Loading HTML into webview...");
                    [webview loadHTMLString:html baseURL:nil];

                    __block int attempts = 0;
                    void (^checkReady)(void) = ^{
                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] checkReady called (attempts=%d)", attempts] UTF8String]);
                        logPDFExport(logPath, "[PDF Export] Evaluating JavaScript: document.readyState");
                        [webview evaluateJavaScript:@"document.readyState" completionHandler:^(id _Nullable value, NSError * _Nullable error) {
                            logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] JavaScript completionHandler called (value=%@, error=%@)", value ? value : @"nil", error ? error.localizedDescription : @"nil"] UTF8String]);
                            if (error) {
                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] JavaScript evaluation error: %@", error.localizedDescription] UTF8String]);
                            }
                            BOOL ready = [(NSString*)value isEqualToString:@"complete"];
                            logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Document readyState check: ready=%@, attempts=%d, isComplete=%d", value, attempts, ready] UTF8String]);
                            if (ready || attempts > 50) {
                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Document ready or max attempts reached (ready=%d, attempts=%d), creating PDF...", ready, attempts] UTF8String]);
                                @try {
                                    if (@available(macOS 11.0, *)) {
                                        WKPDFConfiguration* pdfConfig = [WKPDFConfiguration new];
                                        logPDFExport(logPath, "[PDF Export] Calling createPDFWithConfiguration...");
                                        [webview createPDFWithConfiguration:pdfConfig completionHandler:^(NSData * _Nullable pdfData, NSError * _Nullable error2) {
                                            logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] createPDF completionHandler called (pdfData=%@, error=%@)", pdfData ? @"not nil" : @"nil", error2 ? error2.localizedDescription : @"nil"] UTF8String]);
                                            if (error2 || !pdfData) {
                                                NSString* msg = error2 ? [NSString stringWithFormat:@"PDF creation error: %@", error2.localizedDescription] : @"No PDF data";
                                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] ERROR: %@", msg] UTF8String]);
                                                retErr = strdup([msg UTF8String]);
                                            } else {
                                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] PDF data created, size: %lu bytes", (unsigned long)[pdfData length]] UTF8String]);
                                                NSError* writeErr = nil;
                                                BOOL writeSuccess = [pdfData writeToFile:outPath options:NSDataWritingAtomic error:&writeErr];
                                                if (writeErr || !writeSuccess) {
                                                    NSString* msg = writeErr ? [NSString stringWithFormat:@"File write error: %@", writeErr.localizedDescription] : @"File write failed (unknown error)";
                                                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] ERROR: %@", msg] UTF8String]);
                                                    retErr = strdup([msg UTF8String]);
                                                } else {
                                                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] PDF file written successfully to: %@", outPath] UTF8String]);
                                                }
                                            }
                                            finished = YES;
                                            dispatch_semaphore_signal(sem);
                                        }];
                                    } else {
                                        NSString* msg = @"macOS 11+ required for WKWebView PDF";
                                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] ERROR: %@", msg] UTF8String]);
                                        retErr = strdup([msg UTF8String]);
                                        finished = YES;
                                        dispatch_semaphore_signal(sem);
                                    }
                                } @catch (NSException* ex) {
                                    NSString* msg = [NSString stringWithFormat:@"Exception: %@", ex.reason];
                                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] EXCEPTION: %@", msg] UTF8String]);
                                    retErr = strdup([msg UTF8String]);
                                    finished = YES;
                                    dispatch_semaphore_signal(sem);
                                }
                            } else {
                                attempts++;
                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Retrying... (attempts=%d, readyState=%@)", attempts, value] UTF8String]);
                                // interactive状態が続く場合は、強制的にPDF作成を試みる
                                if (attempts >= 10 && [(NSString*)value isEqualToString:@"interactive"]) {
                                    logPDFExport(logPath, "[PDF Export] readyState stuck at 'interactive', forcing PDF creation...");
                                    // 強制的にPDF作成を試みる
                                    if (@available(macOS 11.0, *)) {
                                        WKPDFConfiguration* pdfConfig = [WKPDFConfiguration new];
                                        logPDFExport(logPath, "[PDF Export] Forced: Calling createPDFWithConfiguration...");
                                        [webview createPDFWithConfiguration:pdfConfig completionHandler:^(NSData * _Nullable pdfData, NSError * _Nullable error2) {
                                            logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Forced: createPDF completionHandler called (pdfData=%@, error=%@)", pdfData ? @"not nil" : @"nil", error2 ? error2.localizedDescription : @"nil"] UTF8String]);
                                            if (error2 || !pdfData) {
                                                NSString* msg = error2 ? [NSString stringWithFormat:@"PDF creation error (forced): %@", error2.localizedDescription] : @"No PDF data (forced)";
                                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] ERROR: %@", msg] UTF8String]);
                                                retErr = strdup([msg UTF8String]);
                                            } else {
                                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] PDF data created (forced), size: %lu bytes", (unsigned long)[pdfData length]] UTF8String]);
                                                NSError* writeErr = nil;
                                                BOOL writeSuccess = [pdfData writeToFile:outPath options:NSDataWritingAtomic error:&writeErr];
                                                if (writeErr || !writeSuccess) {
                                                    NSString* msg = writeErr ? [NSString stringWithFormat:@"File write error (forced): %@", writeErr.localizedDescription] : @"File write failed (forced, unknown error)";
                                                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] ERROR: %@", msg] UTF8String]);
                                                    retErr = strdup([msg UTF8String]);
                                                } else {
                                                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] PDF file written successfully (forced) to: %@", outPath] UTF8String]);
                                                }
                                            }
                                            finished = YES;
                                            dispatch_semaphore_signal(sem);
                                        }];
                                    }
                                } else {
                                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Scheduling retry in 0.1s (attempts=%d)", attempts] UTF8String]);
                                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.1 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Retry timer fired (attempts=%d)", attempts] UTF8String]);
                                        checkReady();
                                    });
                                }
                            }
                        }];
                    };
                    // Start readiness polling shortly after load
                    logPDFExport(logPath, "[PDF Export] Starting readiness polling...");
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.15 * NSEC_PER_SEC)), dispatch_get_main_queue(), checkReady);

                    // Fallback: force PDF after 2 seconds even if readyState polling fails
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(2 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Fallback timer fired (finished=%d)", finished] UTF8String]);
                        if (finished) {
                            logPDFExport(logPath, "[PDF Export] Fallback timer: already finished");
                            return;
                        }
                        logPDFExport(logPath, "[PDF Export] Fallback timer: forcing PDF creation...");
                                    if (@available(macOS 11.0, *)) {
                                        WKPDFConfiguration* pdfConfig = [WKPDFConfiguration new];
                                        logPDFExport(logPath, "[PDF Export] Fallback: Calling createPDFWithConfiguration...");
                                        [webview createPDFWithConfiguration:pdfConfig completionHandler:^(NSData * _Nullable pdfData, NSError * _Nullable error2) {
                                            logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Fallback: createPDF completionHandler called (finished=%d, pdfData=%@, error=%@)", finished, pdfData ? @"not nil" : @"nil", error2 ? error2.localizedDescription : @"nil"] UTF8String]);
                                            if (finished) {
                                                logPDFExport(logPath, "[PDF Export] Fallback: already finished, ignoring");
                                                return;
                                            }
                                            if (error2 || !pdfData) {
                                    NSString* msg = error2 ? [NSString stringWithFormat:@"PDF creation error (fallback): %@", error2.localizedDescription] : @"No PDF data (fallback)";
                                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] ERROR (fallback): %@", msg] UTF8String]);
                                    retErr = strdup([msg UTF8String]);
                                } else {
                                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] PDF data created (fallback), size: %lu bytes", (unsigned long)[pdfData length]] UTF8String]);
                                    NSError* writeErr = nil;
                                    BOOL writeSuccess = [pdfData writeToFile:outPath options:NSDataWritingAtomic error:&writeErr];
                                    if (writeErr || !writeSuccess) {
                                        NSString* msg = writeErr ? [NSString stringWithFormat:@"File write error (fallback): %@", writeErr.localizedDescription] : @"File write failed (fallback, unknown error)";
                                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] ERROR (fallback): %@", msg] UTF8String]);
                                        retErr = strdup([msg UTF8String]);
                                    } else {
                                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] PDF file written successfully (fallback) to: %@", outPath] UTF8String]);
                                    }
                                }
                                finished = YES;
                                dispatch_semaphore_signal(sem);
                            }];
                        }
                    });

                    // Hard timeout 10s
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(10 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Hard timeout timer fired (finished=%d)", finished] UTF8String]);
                        if (!finished) {
                            logPDFExport(logPath, "[PDF Export] ERROR: Timeout after 10 seconds");
                            retErr = strdup("PDF export timeout after 10 seconds");
                            finished = YES;
                            dispatch_semaphore_signal(sem);
                        }
                    });

                    // signal happens in createPDF completion or timeouts above
                    [window orderOut:nil];
                    [window setContentView:nil];
                } @catch (NSException* ex) {
                    NSString* msg = [NSString stringWithFormat:@"Outer exception: %@", ex.reason];
                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] OUTER EXCEPTION: %@", msg] UTF8String]);
                    retErr = strdup([msg UTF8String]);
                    finished = YES;
                    dispatch_semaphore_signal(sem);
                }
            }
            // Note: semaphore is signaled in PDF completion handlers or timeout handlers above
            // Do NOT signal here as it would cause early return before PDF generation completes
        });

        // Wait for async block to finish (including its inner sem signal)
        logPDFExport(logPath, "[PDF Export] Waiting for PDF generation to complete...");
        while (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.05 * NSEC_PER_SEC)))) {
            [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
        }
        if (retErr) {
            logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Returning error: %s", retErr] UTF8String]);
        } else {
            logPDFExport(logPath, "[PDF Export] PDF export completed successfully");
        }
        return retErr;
    }
}
*/
import "C"
import (
	"fmt"
	"runtime"
	"unsafe"
)

// ExportHTMLToPDF renders HTML to a PDF at outPath using WKWebView
func ExportHTMLToPDF(html string, outPath string, logPath string) error {
	// AppKit要件: メインスレッドで実行
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cHtml := C.CString(html)
	defer C.free(unsafe.Pointer(cHtml))
	cOut := C.CString(outPath)
	defer C.free(unsafe.Pointer(cOut))
	cLogPath := C.CString(logPath)
	defer C.free(unsafe.Pointer(cLogPath))

	cerr := C.exportHTMLToPDFMac(cHtml, cOut, cLogPath)
	if cerr != nil {
		defer C.free(unsafe.Pointer(cerr))
		return fmt.Errorf(C.GoString(cerr))
	}
	return nil
}
