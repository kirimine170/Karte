//go:build darwin

package pdf

/*
#cgo darwin CFLAGS: -x objective-c -fobjc-arc -fmodules
#cgo darwin LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

static const char* exportHTMLToPDFMac(const char* htmlC, const char* outPathC) {
    @autoreleasepool {
        NSString* html = [NSString stringWithUTF8String:htmlC ? htmlC : ""];
        NSString* outPath = [NSString stringWithUTF8String:outPathC ? outPathC : ""];
        __block const char* retErr = NULL;

        NSLog(@"[PDF Export] Starting PDF export to: %@", outPath);
        NSLog(@"[PDF Export] HTML length: %lu", (unsigned long)[html length]);

        // Execute on main thread asynchronously to avoid deadlock if caller is already on main thread
        dispatch_semaphore_t sem = dispatch_semaphore_create(0);
        dispatch_async(dispatch_get_main_queue(), ^{
            @autoreleasepool {
                __block BOOL finished = NO;

                @try {
                    NSLog(@"[PDF Export] Initializing NSApplication...");
                    if ([NSApplication sharedApplication] == nil) {
                        [NSApplication sharedApplication];
                    }
                    NSLog(@"[PDF Export] Creating WKWebView...");
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

                    NSLog(@"[PDF Export] Loading HTML into webview...");
                    [webview loadHTMLString:html baseURL:nil];

                    __block int attempts = 0;
                    void (^checkReady)(void) = ^{
                        [webview evaluateJavaScript:@"document.readyState" completionHandler:^(id _Nullable value, NSError * _Nullable error) {
                            if (error) {
                                NSLog(@"[PDF Export] JavaScript evaluation error: %@", error.localizedDescription);
                            }
                            BOOL ready = [(NSString*)value isEqualToString:@"complete"];
                            NSLog(@"[PDF Export] Document readyState check: ready=%@, attempts=%d", value, attempts);
                            if (ready || attempts > 50) {
                                NSLog(@"[PDF Export] Document ready, creating PDF...");
                                @try {
                                    if (@available(macOS 11.0, *)) {
                                        WKPDFConfiguration* pdfConfig = [WKPDFConfiguration new];
                                        [webview createPDFWithConfiguration:pdfConfig completionHandler:^(NSData * _Nullable pdfData, NSError * _Nullable error2) {
                                            if (error2 || !pdfData) {
                                                NSString* msg = error2 ? [NSString stringWithFormat:@"PDF creation error: %@", error2.localizedDescription] : @"No PDF data";
                                                NSLog(@"[PDF Export] ERROR: %@", msg);
                                                retErr = strdup([msg UTF8String]);
                                            } else {
                                                NSLog(@"[PDF Export] PDF data created, size: %lu bytes", (unsigned long)[pdfData length]);
                                                NSError* writeErr = nil;
                                                BOOL writeSuccess = [pdfData writeToFile:outPath options:NSDataWritingAtomic error:&writeErr];
                                                if (writeErr || !writeSuccess) {
                                                    NSString* msg = writeErr ? [NSString stringWithFormat:@"File write error: %@", writeErr.localizedDescription] : @"File write failed (unknown error)";
                                                    NSLog(@"[PDF Export] ERROR: %@", msg);
                                                    retErr = strdup([msg UTF8String]);
                                                } else {
                                                    NSLog(@"[PDF Export] PDF file written successfully to: %@", outPath);
                                                }
                                            }
                                            finished = YES;
                                            dispatch_semaphore_signal(sem);
                                        }];
                                    } else {
                                        NSString* msg = @"macOS 11+ required for WKWebView PDF";
                                        NSLog(@"[PDF Export] ERROR: %@", msg);
                                        retErr = strdup([msg UTF8String]);
                                        finished = YES;
                                        dispatch_semaphore_signal(sem);
                                    }
                                } @catch (NSException* ex) {
                                    NSString* msg = [NSString stringWithFormat:@"Exception: %@", ex.reason];
                                    NSLog(@"[PDF Export] EXCEPTION: %@", msg);
                                    retErr = strdup([msg UTF8String]);
                                    finished = YES;
                                    dispatch_semaphore_signal(sem);
                                }
                            } else {
                                attempts++;
                                dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.1 * NSEC_PER_SEC)), dispatch_get_main_queue(), checkReady);
                            }
                        }];
                    };
                    // Start readiness polling shortly after load
                    NSLog(@"[PDF Export] Starting readiness polling...");
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.15 * NSEC_PER_SEC)), dispatch_get_main_queue(), checkReady);

                    // Fallback: force PDF after 2 seconds even if readyState polling fails
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(2 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                        if (finished) {
                            NSLog(@"[PDF Export] Fallback timer: already finished");
                            return;
                        }
                        NSLog(@"[PDF Export] Fallback timer: forcing PDF creation...");
                        if (@available(macOS 11.0, *)) {
                            WKPDFConfiguration* pdfConfig = [WKPDFConfiguration new];
                            [webview createPDFWithConfiguration:pdfConfig completionHandler:^(NSData * _Nullable pdfData, NSError * _Nullable error2) {
                                if (finished) {
                                    NSLog(@"[PDF Export] Fallback: already finished, ignoring");
                                    return;
                                }
                                if (error2 || !pdfData) {
                                    NSString* msg = error2 ? [NSString stringWithFormat:@"PDF creation error (fallback): %@", error2.localizedDescription] : @"No PDF data (fallback)";
                                    NSLog(@"[PDF Export] ERROR (fallback): %@", msg);
                                    retErr = strdup([msg UTF8String]);
                                } else {
                                    NSLog(@"[PDF Export] PDF data created (fallback), size: %lu bytes", (unsigned long)[pdfData length]);
                                    NSError* writeErr = nil;
                                    BOOL writeSuccess = [pdfData writeToFile:outPath options:NSDataWritingAtomic error:&writeErr];
                                    if (writeErr || !writeSuccess) {
                                        NSString* msg = writeErr ? [NSString stringWithFormat:@"File write error (fallback): %@", writeErr.localizedDescription] : @"File write failed (fallback, unknown error)";
                                        NSLog(@"[PDF Export] ERROR (fallback): %@", msg);
                                        retErr = strdup([msg UTF8String]);
                                    } else {
                                        NSLog(@"[PDF Export] PDF file written successfully (fallback) to: %@", outPath);
                                    }
                                }
                                finished = YES;
                                dispatch_semaphore_signal(sem);
                            }];
                        }
                    });

                    // Hard timeout 10s
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(10 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                        if (!finished) {
                            NSLog(@"[PDF Export] ERROR: Timeout after 10 seconds");
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
                    NSLog(@"[PDF Export] OUTER EXCEPTION: %@", msg);
                    retErr = strdup([msg UTF8String]);
                    finished = YES;
                    dispatch_semaphore_signal(sem);
                }
            }
            // Note: semaphore is signaled in PDF completion handlers or timeout handlers above
            // Do NOT signal here as it would cause early return before PDF generation completes
        });

        // Wait for async block to finish (including its inner sem signal)
        NSLog(@"[PDF Export] Waiting for PDF generation to complete...");
        while (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.05 * NSEC_PER_SEC)))) {
            [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
        }
        if (retErr) {
            NSLog(@"[PDF Export] Returning error: %s", retErr);
        } else {
            NSLog(@"[PDF Export] PDF export completed successfully");
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
func ExportHTMLToPDF(html string, outPath string) error {
	// AppKit要件: メインスレッドで実行
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cHtml := C.CString(html)
	defer C.free(unsafe.Pointer(cHtml))
	cOut := C.CString(outPath)
	defer C.free(unsafe.Pointer(cOut))

	cerr := C.exportHTMLToPDFMac(cHtml, cOut)
	if cerr != nil {
		defer C.free(unsafe.Pointer(cerr))
		return fmt.Errorf(C.GoString(cerr))
	}
	return nil
}
