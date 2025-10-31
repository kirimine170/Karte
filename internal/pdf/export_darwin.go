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

        // Execute on main thread asynchronously to avoid deadlock if caller is already on main thread
        dispatch_semaphore_t sem = dispatch_semaphore_create(0);
        dispatch_async(dispatch_get_main_queue(), ^{
            @autoreleasepool {
                __block BOOL finished = NO;

                if ([NSApplication sharedApplication] == nil) {
                    [NSApplication sharedApplication];
                }
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

                [webview loadHTMLString:html baseURL:nil];

                __block int attempts = 0;
                void (^checkReady)(void) = ^{
                    [webview evaluateJavaScript:@"document.readyState" completionHandler:^(id _Nullable value, NSError * _Nullable error) {
                        BOOL ready = [(NSString*)value isEqualToString:@"complete"];
                        if (ready || attempts > 50) {
                            @try {
                                if (@available(macOS 11.0, *)) {
                                    WKPDFConfiguration* pdfConfig = [WKPDFConfiguration new];
                                    [webview createPDFWithConfiguration:pdfConfig completionHandler:^(NSData * _Nullable pdfData, NSError * _Nullable error2) {
                                        if (error2 || !pdfData) {
                                            NSString* msg = error2 ? error2.localizedDescription : @"No PDF data";
                                            retErr = strdup([msg UTF8String]);
                                        } else {
                                            NSError* writeErr = nil;
                                            [pdfData writeToFile:outPath options:NSDataWritingAtomic error:&writeErr];
                                            if (writeErr) {
                                                retErr = strdup([[writeErr localizedDescription] UTF8String]);
                                            }
                                        }
                                        finished = YES;
                                        dispatch_semaphore_signal(sem);
                                    }];
                                } else {
                                    retErr = strdup("macOS 11+ required for WKWebView PDF");
                                    finished = YES;
                                    dispatch_semaphore_signal(sem);
                                }
                            } @catch (NSException* ex) {
                                retErr = strdup([[ex reason] UTF8String]);
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
                dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.15 * NSEC_PER_SEC)), dispatch_get_main_queue(), checkReady);

                // Fallback: force PDF after 2 seconds even if readyState polling fails
                dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(2 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                    if (finished) return;
                    if (@available(macOS 11.0, *)) {
                        WKPDFConfiguration* pdfConfig = [WKPDFConfiguration new];
                        [webview createPDFWithConfiguration:pdfConfig completionHandler:^(NSData * _Nullable pdfData, NSError * _Nullable error2) {
                            if (finished) return;
                            if (error2 || !pdfData) {
                                NSString* msg = error2 ? error2.localizedDescription : @"No PDF data (fallback)";
                                retErr = strdup([msg UTF8String]);
                            } else {
                                NSError* writeErr = nil;
                                [pdfData writeToFile:outPath options:NSDataWritingAtomic error:&writeErr];
                                if (writeErr) {
                                    retErr = strdup([[writeErr localizedDescription] UTF8String]);
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
                        retErr = strdup("PDF export timeout");
                        finished = YES;
                        dispatch_semaphore_signal(sem);
                    }
                });

                // signal happens in createPDF completion or timeouts above
                [window orderOut:nil];
                [window setContentView:nil];
            }
            dispatch_semaphore_signal(sem);
        });

        // Wait for async block to finish (including its inner sem signal)
        while (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.05 * NSEC_PER_SEC)))) {
            [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
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
