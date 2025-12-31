//go:build darwin

package pdf

/*
#cgo darwin CFLAGS: -x objective-c -fobjc-arc -fmodules
#cgo darwin LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <time.h>
#include <string.h>
#include <mach/mach.h>
#include <mach/task_info.h>

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

// 検証用ログファイルに出力するヘルパー関数
static void logPDFDebug(const char* logPath, const char* msg) {
    if (logPath == NULL || strlen(logPath) == 0) {
        return;
    }

    // 検証用ログファイルのパスを生成（元のログファイルと同じディレクトリに pdf-export-debug.log を作成）
    NSString* logPathStr = [NSString stringWithUTF8String:logPath];
    NSString* logDir = [logPathStr stringByDeletingLastPathComponent];
    NSString* debugLogPath = [logDir stringByAppendingPathComponent:@"pdf-export-debug.log"];

    FILE *debugLogFile = fopen([debugLogPath UTF8String], "a");
    if (debugLogFile == NULL) {
        return;
    }

    // タイムスタンプを生成
    time_t now = time(NULL);
    struct tm *tm_info = localtime(&now);
    char timestamp[64];
    long offset = tm_info->tm_gmtoff;
    int hours = (int)(offset / 3600);
    int minutes = (int)((offset % 3600) / 60);
    if (minutes < 0) minutes = -minutes;

    strftime(timestamp, sizeof(timestamp), "%Y-%m-%dT%H:%M:%S", tm_info);
    fprintf(debugLogFile, "%s%+03d:%02d [DEBUG] %s\n", timestamp, hours, minutes, msg);
    fflush(debugLogFile);
    fclose(debugLogFile);
}

// メモリ使用量を取得してログに記録するヘルパー関数
static void logMemoryUsage(const char* logPath) {
    struct task_basic_info info;
    mach_msg_type_number_t size = sizeof(info);
    kern_return_t kerr = task_info(mach_task_self(), TASK_BASIC_INFO, (task_info_t)&info, &size);

    if (kerr == KERN_SUCCESS) {
        // メモリ使用量をMB単位で計算
        double residentSizeMB = (double)info.resident_size / (1024.0 * 1024.0);
        double virtualSizeMB = (double)info.virtual_size / (1024.0 * 1024.0);
        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Memory usage: resident=%.2f MB, virtual=%.2f MB", residentSizeMB, virtualSizeMB] UTF8String]);
    } else {
        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Failed to get memory info: kern_return=%d", kerr] UTF8String]);
    }
}

// WKNavigationDelegateとWKUIDelegateを実装するクラス
@interface PDFExportDelegate : NSObject <WKNavigationDelegate, WKUIDelegate>
@property (nonatomic, assign) const char* logPath;
@property (nonatomic, copy) void (^checkReadyBlock)(void);
@end

@implementation PDFExportDelegate

- (void)webView:(WKWebView *)webView didStartProvisionalNavigation:(WKNavigation *)navigation {
    logPDFExport(self.logPath, "[PDF Export] Navigation started (provisional)");
    logPDFDebug(self.logPath, "========== didStartProvisionalNavigation ==========");
    logPDFDebug(self.logPath, [[NSString stringWithFormat:@"URL: %@", webView.URL ? webView.URL.absoluteString : @"nil"] UTF8String]);
    logPDFDebug(self.logPath, [[NSString stringWithFormat:@"Title: %@", webView.title ? webView.title : @"nil"] UTF8String]);
    logPDFDebug(self.logPath, [[NSString stringWithFormat:@"isLoading: %d", webView.loading ? 1 : 0] UTF8String]);
    logPDFDebug(self.logPath, "========== END didStartProvisionalNavigation ==========");
}

- (void)webView:(WKWebView *)webView didCommitNavigation:(WKNavigation *)navigation {
    logPDFExport(self.logPath, "[PDF Export] Navigation committed");
    logMemoryUsage(self.logPath);
    logPDFDebug(self.logPath, "========== didCommitNavigation ==========");
    logPDFDebug(self.logPath, [[NSString stringWithFormat:@"URL: %@", webView.URL ? webView.URL.absoluteString : @"nil"] UTF8String]);
    logPDFDebug(self.logPath, [[NSString stringWithFormat:@"Title: %@", webView.title ? webView.title : @"nil"] UTF8String]);
    logPDFDebug(self.logPath, [[NSString stringWithFormat:@"isLoading: %d", webView.loading ? 1 : 0] UTF8String]);

    // DOM構造を確認するJavaScriptを実行
    NSString* domCheckScript = @"(function() { \
        return { \
            readyState: document.readyState, \
            htmlLength: document.documentElement ? document.documentElement.innerHTML.length : 0, \
            bodyLength: document.body ? document.body.innerHTML.length : 0, \
            hasHtmlTag: document.documentElement ? 1 : 0, \
            hasBodyTag: document.body ? 1 : 0, \
            title: document.title || '', \
            url: window.location.href || '' \
        }; \
    })();";

    [webView evaluateJavaScript:domCheckScript completionHandler:^(id result, NSError* error) {
        if (error) {
            logPDFDebug(self.logPath, [[NSString stringWithFormat:@"JavaScript evaluation error in didCommitNavigation: %@", error.localizedDescription] UTF8String]);
        } else {
            logPDFDebug(self.logPath, [[NSString stringWithFormat:@"DOM state in didCommitNavigation: %@", result] UTF8String]);
        }
    }];

    logPDFDebug(self.logPath, "========== END didCommitNavigation ==========");
}

- (void)webView:(WKWebView *)webView didFailProvisionalNavigation:(WKNavigation *)navigation withError:(NSError *)error {
    logPDFExport(self.logPath, [[NSString stringWithFormat:@"[PDF Export] Navigation failed: %@ (code=%ld, domain=%@)", error.localizedDescription, (long)error.code, error.domain] UTF8String]);
    if (error.userInfo) {
        logPDFExport(self.logPath, [[NSString stringWithFormat:@"[PDF Export] Navigation error details: %@", error.userInfo] UTF8String]);
    }
    logMemoryUsage(self.logPath); // エラー時にもメモリ使用量を記録
}

- (void)webView:(WKWebView *)webView didFailNavigation:(WKNavigation *)navigation withError:(NSError *)error {
    logPDFExport(self.logPath, [[NSString stringWithFormat:@"[PDF Export] Navigation error: %@ (code=%ld, domain=%@)", error.localizedDescription, (long)error.code, error.domain] UTF8String]);
    if (error.userInfo) {
        logPDFExport(self.logPath, [[NSString stringWithFormat:@"[PDF Export] Navigation error details: %@", error.userInfo] UTF8String]);
    }
    logMemoryUsage(self.logPath);
}

- (void)webView:(WKWebView *)webView didFinishNavigation:(WKNavigation *)navigation {
    logPDFExport(self.logPath, "[PDF Export] Navigation finished - starting readiness check");
    logMemoryUsage(self.logPath);
    logPDFDebug(self.logPath, "========== didFinishNavigation ==========");
    logPDFDebug(self.logPath, [[NSString stringWithFormat:@"URL: %@", webView.URL ? webView.URL.absoluteString : @"nil"] UTF8String]);
    logPDFDebug(self.logPath, [[NSString stringWithFormat:@"Title: %@", webView.title ? webView.title : @"nil"] UTF8String]);
    logPDFDebug(self.logPath, [[NSString stringWithFormat:@"isLoading: %d", webView.loading ? 1 : 0] UTF8String]);

    // DOM構造を詳細に確認するJavaScriptを実行
    NSString* detailedDomCheckScript = @"(function() { \
        var html = document.documentElement; \
        var body = document.body; \
        return { \
            readyState: document.readyState, \
            htmlLength: html ? html.innerHTML.length : 0, \
            bodyLength: body ? body.innerHTML.length : 0, \
            hasHtmlTag: html ? 1 : 0, \
            hasBodyTag: body ? 1 : 0, \
            title: document.title || '', \
            url: window.location.href || '', \
            htmlOuterHTML: html ? html.outerHTML.substring(0, 500) : '', \
            bodyOuterHTML: body ? body.outerHTML.substring(0, 500) : '', \
            imgCount: document.querySelectorAll('img').length, \
            allElementsCount: document.querySelectorAll('*').length \
        }; \
    })();";

    [webView evaluateJavaScript:detailedDomCheckScript completionHandler:^(id result, NSError* error) {
        if (error) {
            logPDFDebug(self.logPath, [[NSString stringWithFormat:@"JavaScript evaluation error in didFinishNavigation: %@", error.localizedDescription] UTF8String]);
        } else {
            logPDFDebug(self.logPath, [[NSString stringWithFormat:@"Detailed DOM state in didFinishNavigation: %@", result] UTF8String]);
        }
    }];

    logPDFDebug(self.logPath, "========== END didFinishNavigation ==========");

    // ナビゲーション完了後にcheckReadyを実行
    if (self.checkReadyBlock) {
        dispatch_async(dispatch_get_main_queue(), ^{
            self.checkReadyBlock();
        });
    }
}

- (void)webView:(WKWebView *)webView runJavaScriptAlertPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(void))completionHandler {
    logPDFExport(self.logPath, [[NSString stringWithFormat:@"[PDF Export] JavaScript alert: %@", message] UTF8String]);
    completionHandler();
}

- (void)webView:(WKWebView *)webView runJavaScriptConfirmPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(BOOL))completionHandler {
    logPDFExport(self.logPath, [[NSString stringWithFormat:@"[PDF Export] JavaScript confirm: %@", message] UTF8String]);
    completionHandler(YES);
}

- (void)webView:(WKWebView *)webView runJavaScriptTextInputPanelWithPrompt:(NSString *)prompt defaultText:(NSString *)defaultText initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(NSString * _Nullable))completionHandler {
    logPDFExport(self.logPath, [[NSString stringWithFormat:@"[PDF Export] JavaScript prompt: %@ (default: %@)", prompt, defaultText] UTF8String]);
    completionHandler(defaultText);
}

- (void)webViewWebContentProcessDidTerminate:(WKWebView *)webView {
    logPDFExport(self.logPath, "[PDF Export] WARNING: Web content process terminated (likely due to memory pressure or crash)");
    logMemoryUsage(self.logPath);
}

- (void)webView:(WKWebView *)webView decidePolicyForNavigationAction:(WKNavigationAction *)navigationAction decisionHandler:(void (^)(WKNavigationActionPolicy))decisionHandler {
    // ナビゲーションアクションをログに記録（デバッグ用）
    if (navigationAction.request.URL) {
        logPDFExport(self.logPath, [[NSString stringWithFormat:@"[PDF Export] Navigation action: %@", navigationAction.request.URL.absoluteString] UTF8String]);
    }
    decisionHandler(WKNavigationActionPolicyAllow);
}

- (void)webView:(WKWebView *)webView decidePolicyForNavigationResponse:(WKNavigationResponse *)navigationResponse decisionHandler:(void (^)(WKNavigationResponsePolicy))decisionHandler {
    // HTTPエラーを検出
    NSHTTPURLResponse* httpResponse = (NSHTTPURLResponse*)navigationResponse.response;
    if (httpResponse && [httpResponse isKindOfClass:[NSHTTPURLResponse class]]) {
        if (httpResponse.statusCode >= 400) {
            logPDFExport(self.logPath, [[NSString stringWithFormat:@"[PDF Export] HTTP error: status=%ld, URL=%@", (long)httpResponse.statusCode, httpResponse.URL.absoluteString] UTF8String]);
        }
    }
    decisionHandler(WKNavigationResponsePolicyAllow);
}

@end

// JavaScriptのconsole.logをキャッチするUserContentController
@interface PDFExportScriptMessageHandler : NSObject <WKScriptMessageHandler>
@property (nonatomic, assign) const char* logPath;
@end

@implementation PDFExportScriptMessageHandler

- (void)userContentController:(WKUserContentController *)userContentController didReceiveScriptMessage:(WKScriptMessage *)message {
    NSString* logMessage = [NSString stringWithFormat:@"[PDF Export] JavaScript console.%@: %@", message.name, message.body];
    logPDFExport(self.logPath, [logMessage UTF8String]);
}

@end

// WKWebView / Window / Delegate を強参照保持するための static 変数
static NSWindow* sPDFWindow = nil;
static WKWebView* sPDFWebView = nil;
static PDFExportDelegate* sPDFDelegate = nil;

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

                    // UserContentControllerを設定してJavaScriptのconsole.logをキャッチ
                    WKUserContentController* userContentController = [WKUserContentController new];
                    PDFExportScriptMessageHandler* scriptHandler = [[PDFExportScriptMessageHandler alloc] init];
                    scriptHandler.logPath = logPath;
                    [userContentController addScriptMessageHandler:scriptHandler name:@"log"];
                    [userContentController addScriptMessageHandler:scriptHandler name:@"error"];
                    [userContentController addScriptMessageHandler:scriptHandler name:@"warn"];
                    [userContentController addScriptMessageHandler:scriptHandler name:@"info"];
                    [config setUserContentController:userContentController];

                    // JavaScriptを注入してconsole.logをキャッチ
                    NSString* scriptSource = @"(function() {"
                        "const originalLog = console.log;"
                        "const originalError = console.error;"
                        "const originalWarn = console.warn;"
                        "const originalInfo = console.info;"
                        "console.log = function(...args) {"
                        "  if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.log) {"
                        "    window.webkit.messageHandlers.log.postMessage(args.map(arg => typeof arg === 'object' ? JSON.stringify(arg) : String(arg)).join(' '));"
                        "  }"
                        "  originalLog.apply(console, arguments);"
                        "};"
                        "console.error = function(...args) {"
                        "  if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.error) {"
                        "    window.webkit.messageHandlers.error.postMessage(args.map(arg => typeof arg === 'object' ? JSON.stringify(arg) : String(arg)).join(' '));"
                        "  }"
                        "  originalError.apply(console, arguments);"
                        "};"
                        "console.warn = function(...args) {"
                        "  if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.warn) {"
                        "    window.webkit.messageHandlers.warn.postMessage(args.map(arg => typeof arg === 'object' ? JSON.stringify(arg) : String(arg)).join(' '));"
                        "  }"
                        "  originalWarn.apply(console, arguments);"
                        "};"
                        "console.info = function(...args) {"
                        "  if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.info) {"
                        "    window.webkit.messageHandlers.info.postMessage(args.map(arg => typeof arg === 'object' ? JSON.stringify(arg) : String(arg)).join(' '));"
                        "  }"
                        "  originalInfo.apply(console, arguments);"
                        "};"
                    "})();";
                    WKUserScript* userScript = [[WKUserScript alloc] initWithSource:scriptSource injectionTime:WKUserScriptInjectionTimeAtDocumentStart forMainFrameOnly:NO];
                    [userContentController addUserScript:userScript];

                    // 以前のインスタンスがあればクリーンアップ
                    if (sPDFWebView) {
                        sPDFWebView.navigationDelegate = nil;
                        sPDFWebView.UIDelegate = nil;
                        sPDFWebView = nil;
                    }
                    if (sPDFWindow) {
                        [sPDFWindow close];
                        sPDFWindow = nil;
                    }
                    sPDFDelegate = nil;

                    // WKWebView / Window / Delegate を static 変数で強参照保持
                    sPDFWebView = [[WKWebView alloc] initWithFrame:NSMakeRect(0,0,800,1000) configuration:config];

                    // デリゲートを設定
                    sPDFDelegate = [[PDFExportDelegate alloc] init];
                    sPDFDelegate.logPath = logPath;
                    sPDFWebView.navigationDelegate = sPDFDelegate;
                    sPDFWebView.UIDelegate = sPDFDelegate;

                    sPDFWindow = [[NSWindow alloc] initWithContentRect:NSMakeRect(0,0,800,1000)
                                                                   styleMask:NSWindowStyleMaskTitled
                                                                     backing:NSBackingStoreBuffered
                                                                       defer:NO];
                    [sPDFWindow setReleasedWhenClosed:NO];
                    [sPDFWindow setOpaque:NO];
                    [sPDFWindow setAlphaValue:0.0];
                    [sPDFWindow setIgnoresMouseEvents:YES];
                    [sPDFWindow setContentView:sPDFWebView];

                    // ローカル変数としても参照を保持（既存コードとの互換性のため）
                    WKWebView* webview = sPDFWebView;
                    PDFExportDelegate* delegate = sPDFDelegate;
                    NSWindow* window = sPDFWindow;

                    logPDFExport(logPath, "[PDF Export] Loading HTML into webview...");

                    // ========== 検証用: HTMLの詳細ダンプ ==========
                    logPDFDebug(logPath, "========== HTML ANALYSIS START ==========");
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"HTML length: %lu bytes", (unsigned long)[html length]] UTF8String]);

                    // HTML構造の分析
                    NSRange htmlTagRange = [html rangeOfString:@"<html" options:NSCaseInsensitiveSearch];
                    NSRange bodyTagRange = [html rangeOfString:@"<body" options:NSCaseInsensitiveSearch];
                    NSRange headTagRange = [html rangeOfString:@"<head" options:NSCaseInsensitiveSearch];
                    NSRange doctypeRange = [html rangeOfString:@"<!doctype" options:NSCaseInsensitiveSearch];

                    logPDFDebug(logPath, [[NSString stringWithFormat:@"HTML structure: <!doctype>=%lu, <html>=%lu, <head>=%lu, <body>=%lu",
                        doctypeRange.location != NSNotFound ? (unsigned long)doctypeRange.location : 0,
                        htmlTagRange.location != NSNotFound ? (unsigned long)htmlTagRange.location : 0,
                        headTagRange.location != NSNotFound ? (unsigned long)headTagRange.location : 0,
                        bodyTagRange.location != NSNotFound ? (unsigned long)bodyTagRange.location : 0] UTF8String]);

                    // 画像タグの分析
                    NSUInteger imgTagCount = 0;
                    NSRange imgSearchRange = NSMakeRange(0, [html length]);
                    while (imgSearchRange.location < [html length]) {
                        NSRange imgRange = [html rangeOfString:@"<img" options:NSCaseInsensitiveSearch range:imgSearchRange];
                        if (imgRange.location != NSNotFound) {
                            imgTagCount++;
                            imgSearchRange.location = imgRange.location + imgRange.length;
                            imgSearchRange.length = [html length] - imgSearchRange.location;
                        } else {
                            break;
                        }
                    }
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"Image tags found: %lu", (unsigned long)imgTagCount] UTF8String]);

                    // data:imageの分析
                    NSUInteger dataImageCount = 0;
                    NSRange dataImageSearchRange = NSMakeRange(0, [html length]);
                    while (dataImageSearchRange.location < [html length]) {
                        NSRange dataImageRange = [html rangeOfString:@"data:image" options:NSCaseInsensitiveSearch range:dataImageSearchRange];
                        if (dataImageRange.location != NSNotFound) {
                            dataImageCount++;
                            // data:imageの最初の200文字を取得
                            NSUInteger previewLength = MIN(200, [html length] - dataImageRange.location);
                            NSString* dataImagePreview = [html substringWithRange:NSMakeRange(dataImageRange.location, previewLength)];
                            logPDFDebug(logPath, [[NSString stringWithFormat:@"data:image[%lu] at position %lu: %@...",
                                (unsigned long)dataImageCount, (unsigned long)dataImageRange.location, dataImagePreview] UTF8String]);
                            dataImageSearchRange.location = dataImageRange.location + dataImageRange.length;
                            dataImageSearchRange.length = [html length] - dataImageSearchRange.location;
                        } else {
                            break;
                        }
                    }
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"Total data:image occurrences: %lu", (unsigned long)dataImageCount] UTF8String]);

                    // HTMLの最初と最後の部分をダンプ
                    NSUInteger previewLength = MIN(2000, [html length]);
                    NSString* htmlStart = [html substringToIndex:previewLength];
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"HTML start (first %lu chars):\n%@", (unsigned long)previewLength, htmlStart] UTF8String]);

                    if ([html length] > 2000) {
                        NSUInteger endStart = [html length] - MIN(1000, [html length] - previewLength);
                        NSString* htmlEnd = [html substringFromIndex:endStart];
                        logPDFDebug(logPath, [[NSString stringWithFormat:@"HTML end (last %lu chars):\n%@", (unsigned long)[htmlEnd length], htmlEnd] UTF8String]);
                    }

                    // 一時ファイルにHTMLを保存して検証
                    NSString* tempDirForDebug = NSTemporaryDirectory();
                    NSString* debugHTMLPath = [tempDirForDebug stringByAppendingPathComponent:[NSString stringWithFormat:@"karte-pdf-debug-%lu.html", (unsigned long)[[NSDate date] timeIntervalSince1970]]];
                    NSError* writeError = nil;
                    BOOL writeSuccess = [html writeToFile:debugHTMLPath atomically:YES encoding:NSUTF8StringEncoding error:&writeError];
                    if (writeSuccess) {
                        logPDFDebug(logPath, [[NSString stringWithFormat:@"HTML saved to temp file for verification: %@", debugHTMLPath] UTF8String]);
                    } else {
                        logPDFDebug(logPath, [[NSString stringWithFormat:@"Failed to save HTML to temp file: %@", writeError ? writeError.localizedDescription : @"unknown error"] UTF8String]);
                    }

                    logPDFDebug(logPath, "========== HTML ANALYSIS END ==========");

                    // 既存のログにも簡易情報を出力
                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] DEBUG: HTML to load - length=%lu", (unsigned long)[html length]] UTF8String]);
                    NSString* htmlPreview = [html length] > 500 ? [[html substringToIndex:500] stringByAppendingString:@"...(truncated)"] : html;
                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] DEBUG: HTML preview (first 500 chars):\n%@", htmlPreview] UTF8String]);

                    NSRange dataImageRange = [html rangeOfString:@"data:image" options:NSCaseInsensitiveSearch];
                    if (dataImageRange.location != NSNotFound) {
                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] DEBUG: HTML contains 'data:image' at position %lu", (unsigned long)dataImageRange.location] UTF8String]);
                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] DEBUG: HTML contains 'data:image' %lu times", (unsigned long)dataImageCount] UTF8String]);
                    } else {
                        logPDFExport(logPath, "[PDF Export] DEBUG: WARNING - HTML does NOT contain 'data:image'!");
                    }

                    logMemoryUsage(logPath); // 読み込み前のメモリ使用量
                    NSDate* loadStartTime = [NSDate date];

                    // baseURLを一時ディレクトリに設定（Data URI画像の相対パス解決のため）
                    NSString* tempDir = NSTemporaryDirectory();
                    NSURL* baseURL = [NSURL fileURLWithPath:tempDir isDirectory:YES];

                    logPDFExport(logPath, "[PDF Export] Loading HTML into webview with loadHTMLString...");
                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] DEBUG: baseURL=%@, html length=%lu", baseURL.absoluteString, (unsigned long)[html length]] UTF8String]);

                    // ========== 検証用: WKWebView読み込み前の状態 ==========
                    logPDFDebug(logPath, "========== WKWebView STATE BEFORE LOAD ==========");
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView URL: %@", webview.URL ? webview.URL.absoluteString : @"nil"] UTF8String]);
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView title: %@", webview.title ? webview.title : @"nil"] UTF8String]);
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView isLoading: %d", webview.loading ? 1 : 0] UTF8String]);
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView estimatedProgress: %.2f", webview.estimatedProgress] UTF8String]);
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"baseURL: %@", baseURL.absoluteString] UTF8String]);
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"html length: %lu bytes", (unsigned long)[html length]] UTF8String]);
                    logPDFDebug(logPath, "========== END WKWebView STATE BEFORE LOAD ==========");

                    [webview loadHTMLString:html baseURL:baseURL];
                    logPDFExport(logPath, "[PDF Export] HTML loaded into webview (loadHTMLString)");
                    logMemoryUsage(logPath); // 読み込み後のメモリ使用量

                    // ========== 検証用: WKWebView読み込み直後の状態 ==========
                    logPDFDebug(logPath, "========== WKWebView STATE IMMEDIATELY AFTER LOAD ==========");
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView URL: %@", webview.URL ? webview.URL.absoluteString : @"nil"] UTF8String]);
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView title: %@", webview.title ? webview.title : @"nil"] UTF8String]);
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView isLoading: %d", webview.loading ? 1 : 0] UTF8String]);
                    logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView estimatedProgress: %.2f", webview.estimatedProgress] UTF8String]);
                    logPDFDebug(logPath, "========== END WKWebView STATE IMMEDIATELY AFTER LOAD ==========");

                    __block int attempts = 0;
                    __block NSDate* startTime = [NSDate date];
                    void (^checkReady)(void) = ^{
                        NSTimeInterval elapsed = [[NSDate date] timeIntervalSinceDate:startTime];
                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] checkReady called (attempts=%d, elapsed=%.2fs)", attempts, elapsed] UTF8String]);
                        fflush(stdout);

                        // メモリ使用量を定期的にログに記録（10回ごと、または5秒ごと）
                        if (attempts % 10 == 0 || (attempts > 0 && elapsed >= 5.0 && (int)elapsed % 5 == 0)) {
                            logMemoryUsage(logPath);
                            fflush(stdout);
                        }

                        logPDFExport(logPath, "[PDF Export] Evaluating JavaScript: checking readyState and image loading...");
                        fflush(stdout);

                        // ========== 検証用: checkReady時のWKWebView状態 ==========
                        logPDFDebug(logPath, [[NSString stringWithFormat:@"========== checkReady (attempts=%d) ==========", attempts] UTF8String]);
                        logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView URL: %@", webview.URL ? webview.URL.absoluteString : @"nil"] UTF8String]);
                        logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView title: %@", webview.title ? webview.title : @"nil"] UTF8String]);
                        logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView isLoading: %d", webview.loading ? 1 : 0] UTF8String]);
                        logPDFDebug(logPath, [[NSString stringWithFormat:@"WKWebView estimatedProgress: %.2f", webview.estimatedProgress] UTF8String]);
                        logPDFDebug(logPath, [[NSString stringWithFormat:@"Elapsed time: %.2f seconds", elapsed] UTF8String]);

                        // 画像の読み込み完了も確認するJavaScript
                        // document.querySelectorAll('img')を使用してData URIの画像も検出
                        // HTMLにdata:imageが含まれているかもチェック
                        NSString* checkScript = @"(function() { \
                            var images = document.querySelectorAll('img'); \
                            var allLoaded = true; \
                            var loadedCount = 0; \
                            var totalCount = images.length; \
                            var imageDetails = []; \
                            for (var i = 0; i < images.length; i++) { \
                                var img = images[i]; \
                                var detail = { \
                                    index: i, \
                                    src: img.src ? img.src.substring(0, 150) : 'no src', \
                                    complete: img.complete, \
                                    naturalWidth: img.naturalWidth, \
                                    naturalHeight: img.naturalHeight, \
                                    width: img.width, \
                                    height: img.height \
                                }; \
                                imageDetails.push(detail); \
                                if (img.complete && img.naturalWidth > 0) { \
                                    loadedCount++; \
                                } else { \
                                    allLoaded = false; \
                                } \
                            } \
                            var htmlContent = document.documentElement.innerHTML; \
                            var hasDataImage = htmlContent.indexOf('data:image') !== -1; \
                            var htmlLength = htmlContent.length; \
                            var htmlPreview = htmlContent.substring(0, Math.min(2000, htmlLength)); \
                            var imgTagCount = (htmlContent.match(/<img/gi) || []).length; \
                            var dataImageCount = (htmlContent.match(/data:image/gi) || []).length; \
                            var bodyHTML = document.body ? document.body.innerHTML.substring(0, Math.min(1000, document.body.innerHTML.length)) : 'no body'; \
                            return { \
                                readyState: document.readyState, \
                                imagesLoaded: allLoaded, \
                                imageCount: totalCount, \
                                loadedImageCount: loadedCount, \
                                hasDataImageInHTML: hasDataImage, \
                                htmlLength: htmlLength, \
                                htmlPreview: htmlPreview, \
                                imgTagCount: imgTagCount, \
                                dataImageCount: dataImageCount, \
                                bodyHTML: bodyHTML, \
                                imageDetails: imageDetails \
                            }; \
                        })()";

                        [webview evaluateJavaScript:checkScript completionHandler:^(id _Nullable value, NSError * _Nullable error) {
                            logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] JavaScript completionHandler called (value=%@, error=%@)", value ? value : @"nil", error ? error.localizedDescription : @"nil"] UTF8String]);
                            fflush(stdout);

                            // ========== 検証用: JavaScript評価結果の詳細ダンプ ==========
                            logPDFDebug(logPath, "========== JavaScript Evaluation Result ==========");
                            if (error) {
                                logPDFDebug(logPath, [[NSString stringWithFormat:@"ERROR: %@ (code=%ld, domain=%@)", error.localizedDescription, (long)error.code, error.domain] UTF8String]);
                                if (error.userInfo) {
                                    logPDFDebug(logPath, [[NSString stringWithFormat:@"Error userInfo: %@", error.userInfo] UTF8String]);
                                }
                            } else {
                                logPDFDebug(logPath, "JavaScript evaluation succeeded");
                                if (value) {
                                    // 結果をJSON形式で出力
                                    NSError* jsonError = nil;
                                    NSData* jsonData = [NSJSONSerialization dataWithJSONObject:value options:NSJSONWritingPrettyPrinted error:&jsonError];
                                    if (jsonData && !jsonError) {
                                        NSString* jsonString = [[NSString alloc] initWithData:jsonData encoding:NSUTF8StringEncoding];
                                        logPDFDebug(logPath, [[NSString stringWithFormat:@"Result JSON:\n%@", jsonString] UTF8String]);
                                    } else {
                                        logPDFDebug(logPath, [[NSString stringWithFormat:@"Result (raw): %@", value] UTF8String]);
                                    }
                                } else {
                                    logPDFDebug(logPath, "Result is nil");
                                }
                            }
                            logPDFDebug(logPath, "========== END JavaScript Evaluation Result ==========");

                            if (error) {
                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] JavaScript evaluation error: %@ (code=%ld, domain=%@)", error.localizedDescription, (long)error.code, error.domain] UTF8String]);
                                if (error.userInfo) {
                                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] JavaScript error details: %@", error.userInfo] UTF8String]);
                                }
                                logMemoryUsage(logPath); // エラー時にもメモリ使用量を記録
                                fflush(stdout);
                            }

                            BOOL ready = NO;
                            NSString* readyState = @"unknown";
                            NSNumber* imagesLoaded = @NO;
                            NSNumber* imageCount = @0;
                            NSNumber* loadedImageCount = @0;
                            NSNumber* hasDataImageInHTML = @NO;

                            if (value && [value isKindOfClass:[NSDictionary class]]) {
                                NSDictionary* result = (NSDictionary*)value;
                                readyState = result[@"readyState"] ?: @"unknown";
                                imagesLoaded = result[@"imagesLoaded"] ?: @NO;
                                imageCount = result[@"imageCount"] ?: @0;
                                loadedImageCount = result[@"loadedImageCount"] ?: @0;
                                hasDataImageInHTML = result[@"hasDataImageInHTML"] ?: @NO;

                                // デバッグ情報を取得
                                NSNumber* htmlLength = result[@"htmlLength"] ?: @0;
                                NSString* htmlPreview = result[@"htmlPreview"] ?: @"";
                                NSNumber* imgTagCount = result[@"imgTagCount"] ?: @0;
                                NSNumber* dataImageCount = result[@"dataImageCount"] ?: @0;
                                NSString* bodyHTML = result[@"bodyHTML"] ?: @"";
                                NSArray* imageDetails = result[@"imageDetails"] ?: @[];

                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] DEBUG: htmlLength=%d, imgTagCount=%d, dataImageCount=%d, imageCount=%d",
                                    [htmlLength intValue], [imgTagCount intValue], [dataImageCount intValue], [imageCount intValue]] UTF8String]);
                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] DEBUG: htmlPreview (first 2000 chars):\n%@", htmlPreview] UTF8String]);
                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] DEBUG: bodyHTML (first 1000 chars):\n%@", bodyHTML] UTF8String]);

                                // 画像の詳細情報をログに出力
                                if ([imageDetails isKindOfClass:[NSArray class]] && [imageDetails count] > 0) {
                                    for (NSDictionary* detail in imageDetails) {
                                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] DEBUG: image[%d]: src=%@, complete=%d, naturalWidth=%d, naturalHeight=%d, width=%d, height=%d",
                                            [[detail objectForKey:@"index"] intValue],
                                            [detail objectForKey:@"src"] ?: @"nil",
                                            [[detail objectForKey:@"complete"] boolValue] ? 1 : 0,
                                            [[detail objectForKey:@"naturalWidth"] intValue],
                                            [[detail objectForKey:@"naturalHeight"] intValue],
                                            [[detail objectForKey:@"width"] intValue],
                                            [[detail objectForKey:@"height"] intValue]] UTF8String]);
                                    }
                                } else {
                                    logPDFExport(logPath, "[PDF Export] DEBUG: No image details found in DOM");
                                }

                                // HTMLにdata:imageが含まれているが、画像が検出されていない場合は待つ
                                if ([hasDataImageInHTML boolValue] && [imageCount intValue] == 0) {
                                    // HTMLに画像が含まれているが、まだDOMに追加されていない
                                    ready = NO;
                                    logPDFExport(logPath, "[PDF Export] HTML contains data:image but images not yet in DOM, waiting...");
                                } else if ([imageCount intValue] == 0) {
                                    // 画像が存在しない場合は、readyState=completeでOK
                                    ready = [readyState isEqualToString:@"complete"];
                                } else {
                                    // 画像が存在する場合は、readyState=completeかつ全画像読み込み完了が必要
                                    ready = [readyState isEqualToString:@"complete"] && [imagesLoaded boolValue];
                                }

                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] readyState=%@, imagesLoaded=%@, imageCount=%@, loadedImageCount=%@, hasDataImageInHTML=%@, ready=%d",
                                    readyState, imagesLoaded, imageCount, loadedImageCount, hasDataImageInHTML, ready] UTF8String]);
                            } else if (value && [value isKindOfClass:[NSString class]]) {
                                // フォールバック: 文字列が返ってきた場合（旧形式）
                                readyState = (NSString*)value;
                                ready = [readyState isEqualToString:@"complete"];
                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] readyState=%@ (fallback mode)", readyState] UTF8String]);
                            }

                            logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Document readyState check: ready=%@, attempts=%d, isComplete=%d", readyState, attempts, ready] UTF8String]);
                            if (ready || attempts > 300) {
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
                                logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Retrying... (attempts=%d, readyState=%@, imagesLoaded=%@)", attempts, readyState, imagesLoaded] UTF8String]);
                                // interactive状態が続く場合は、強制的にPDF作成を試みる
                                // ただし、画像が読み込まれていない場合はさらに待つ
                                if (attempts >= 15 && [readyState isEqualToString:@"interactive"] && [imagesLoaded boolValue]) {
                                    logPDFExport(logPath, "[PDF Export] readyState stuck at 'interactive' but images are loaded, forcing PDF creation...");
                                } else if (attempts >= 20 && [readyState isEqualToString:@"interactive"]) {
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
                                    // リトライ間隔を0.5秒に延長（メインスレッドの負荷を軽減し、大きなHTMLでも処理可能に）
                                    NSTimeInterval elapsed = [[NSDate date] timeIntervalSinceDate:startTime];
                                    logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Scheduling retry in 0.5s (attempts=%d, elapsed=%.2fs)", attempts, elapsed] UTF8String]);
                                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.5 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                                        NSTimeInterval elapsedAfter = [[NSDate date] timeIntervalSinceDate:startTime];
                                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Retry timer fired (attempts=%d, elapsed=%.2fs)", attempts, elapsedAfter] UTF8String]);
                                        fflush(stdout); // ログを確実に出力
                                        checkReady();
                                    });
                                }
                            }
                        }];
                    };

                    // checkReadyブロックをdelegateに設定（didFinishNavigationで呼び出される）
                    delegate.checkReadyBlock = checkReady;
                    logPDFExport(logPath, "[PDF Export] Waiting for navigation to finish before starting readiness check...");

                    // フォールバック: ナビゲーションが完了しない場合に備えて、一定時間後にcheckReadyを呼ぶ
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(2.0 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                        logPDFExport(logPath, "[PDF Export] Fallback: Starting readiness check after 2 seconds (navigation may not have finished)");
                        checkReady();
                    });

                    // Fallback: force PDF after 15 seconds even if readyState polling fails
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(15 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Fallback timer fired (finished=%d)", finished] UTF8String]);
                        logMemoryUsage(logPath); // フォールバックタイマー発火時のメモリ使用量
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

                    // Hard timeout 180s (3 minutes) - 大きなHTMLファイルでも処理できるように長めに設定
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(180 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
                        logPDFExport(logPath, [[NSString stringWithFormat:@"[PDF Export] Hard timeout timer fired (finished=%d)", finished] UTF8String]);
                        if (!finished) {
                            logPDFExport(logPath, "[PDF Export] ERROR: Timeout after 180 seconds");
                            retErr = strdup("PDF export timeout after 180 seconds");
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
		return fmt.Errorf("%s", C.GoString(cerr))
	}
	return nil
}
