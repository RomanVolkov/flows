#import "TerminalView.h"
#include <stdio.h>
#include <stdlib.h>

static int gRunStatus = 0;

@interface ProbeApp : NSObject <NSApplicationDelegate, NSWindowDelegate>
@property(nonatomic, copy) NSString *probeExecutable;
@property(nonatomic, copy) NSString *socketPath;
@property(nonatomic, copy) NSString *sessionID;
@property(nonatomic, strong) NSMutableArray<NSWindow *> *windows;
@property(nonatomic, strong) NSMutableArray<TerminalView *> *views;
@end

@implementation ProbeApp

- (instancetype)init {
    self = [super init];
    if (self) {
        _windows = [NSMutableArray array];
        _views = [NSMutableArray array];
    }
    return self;
}

- (TerminalView *)openWindowWithFrame:(NSRect)contentRect
                             clientID:(NSString *)clientID
                             fontSize:(float)fontSize
                             activate:(BOOL)activate
                               window:(NSWindow **)outWindow {
    NSWindow *win = [[NSWindow alloc] initWithContentRect:contentRect
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskResizable
        backing:NSBackingStoreBuffered defer:NO];
    win.title = [NSString stringWithFormat:@"Flows Terminal (%@)", clientID ?: @"default"];
    win.delegate = self;
    TerminalView *view = [[TerminalView alloc] initWithFrame:contentRect];
    win.contentView = view;
    if (![view attachWithExecutable:self.probeExecutable
                         socketPath:self.socketPath
                          sessionID:self.sessionID
                           clientID:clientID
                              nonce:nil
                              epoch:0
                           fontSize:fontSize]) {
        fprintf(stderr, "Could not attach native Ghostty surface (%s)\n", clientID.UTF8String);
        gRunStatus = 1;
        [NSApp stop:nil];
        return nil;
    }
    [self.windows addObject:win];
    [self.views addObject:view];
    [win center];
    [win makeKeyAndOrderFront:nil];
    [win makeFirstResponder:view];
    if (activate) {
        [NSApp activateIgnoringOtherApps:YES];
    }
    [view syncSize];
    if (outWindow) *outWindow = win;
    return view;
}

- (void)finishWithStatus:(int)status {
    gRunStatus = status;
    for (TerminalView *v in [self.views copy]) {
        [v detach];
    }
    for (NSWindow *w in [self.windows copy]) {
        [w orderOut:nil];
    }
    [NSApp stop:nil];
    NSEvent *event = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
        location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0
        context:nil subtype:0 data1:0 data2:0];
    [NSApp postEvent:event atStart:NO];
}

- (void)windowWillClose:(NSNotification *)notification {
    NSWindow *closed = notification.object;
    NSUInteger idx = [self.windows indexOfObject:closed];
    if (idx != NSNotFound) {
        [self.views[idx] detach];
        [self.views removeObjectAtIndex:idx];
        [self.windows removeObjectAtIndex:idx];
    }
    if (self.windows.count == 0) {
        [self finishWithStatus:gRunStatus];
    }
}

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    (void)notification;
    NSString *mode = NSProcessInfo.processInfo.environment[@"FLOWS_TERMINAL_MODE"] ?: @"fixture";
    if ([mode isEqualToString:@"scrolled"]) {
        [self runScrolledLateAttachTest];
    } else if ([mode isEqualToString:@"ownership"]) {
        [self runOwnershipAndReconnectTest];
    } else if ([mode isEqualToString:@"bench"]) {
        [self runScrollBenchmark];
    } else {
        [self runFixturePhaseTest];
    }
}

- (void)runFixturePhaseTest {
    NSString *phase = NSProcessInfo.processInfo.environment[@"FLOWS_TERMINAL_PHASE"] ?: @"1";
    NSString *marker = NSProcessInfo.processInfo.environment[@"FLOWS_TERMINAL_EXPECT"] ?: @"";
    NSRect contentRect = [phase isEqualToString:@"2"] ? NSMakeRect(0, 0, 700, 420) : NSMakeRect(0, 0, 800, 480);
    NSWindow *window = nil;
    TerminalView *view = [self openWindowWithFrame:contentRect
                                          clientID:[NSString stringWithFormat:@"probe-%@", phase]
                                          fontSize:0
                                          activate:YES
                                            window:&window];
    if (!view) return;

    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:8];
    __block BOOL sent = NO;
    __block BOOL resized = NO;
    [NSTimer scheduledTimerWithTimeInterval:0.05 repeats:YES block:^(NSTimer *timer) {
        NSString *text = [view readScreenText];
        if (!sent && view.ready && view.inputOwner &&
            (marker.length == 0 || [text containsString:marker]) &&
            [text containsString:@"cursor-ok"]) {
            sent = YES;
            NSRange unset = NSMakeRange(NSNotFound, 0);
            [view setMarkedText:@"雪" selectedRange:NSMakeRange(0, 1) replacementRange:unset];
            if (!view.hasMarkedText) {
                [timer invalidate];
                [self finishWithStatus:1];
                return;
            }
            NSString *input = [NSString stringWithFormat:@"typed-雪-%@\n", phase];
            [view insertText:input replacementRange:unset];
            NSEvent *interrupt = [NSEvent keyEventWithType:NSEventTypeKeyDown
                location:NSZeroPoint modifierFlags:NSEventModifierFlagControl
                timestamp:0 windowNumber:window.windowNumber context:nil
                characters:@"\003" charactersIgnoringModifiers:@"c" isARepeat:NO keyCode:8];
            [view keyDown:interrupt];
            ghostty_surface_paste_text(view.surface, "paste", 5);
            ghostty_surface_mouse_pos(view.surface, 60, 100, GHOSTTY_MODS_NONE);
            ghostty_surface_mouse_button(view.surface, GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_LEFT, GHOSTTY_MODS_NONE);
            ghostty_surface_mouse_button(view.surface, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_LEFT, GHOSTTY_MODS_NONE);
        }
        BOOL inputComplete = sent;
        for (NSString *item in @[@"input", @"control", @"paste", @"mouse"]) {
            inputComplete &= [text containsString:[NSString stringWithFormat:@"%@-ok-%@", item, phase]];
        }
        if (inputComplete && !resized) {
            resized = YES;
            NSSize size = [phase isEqualToString:@"1"] ? NSMakeSize(700, 420) : NSMakeSize(760, 440);
            [window setContentSize:size];
        }
        BOOL complete = resized && [text containsString:[NSString stringWithFormat:@"resize-ok-%@", phase]];
        if (complete) {
            fprintf(stdout, "Verified attachment %s to %s\n", phase.UTF8String, marker.UTF8String);
            [timer invalidate];
            [self finishWithStatus:0];
        } else if (deadline.timeIntervalSinceNow <= 0) {
            fprintf(stderr, "Terminal fixture timed out waiting for %s (ready=%d owner=%d). Screen: %s\n",
                marker.UTF8String, view.ready, view.inputOwner, text.UTF8String);
            [timer invalidate];
            [self finishWithStatus:1];
        }
    }];
}

- (void)runScrolledLateAttachTest {
    NSString *expectHistory = NSProcessInfo.processInfo.environment[@"FLOWS_EXPECT_HISTORY"] ?: @"ROW-008";
    NSString *forbidBottom = NSProcessInfo.processInfo.environment[@"FLOWS_FORBID_BOTTOM"] ?: @"LIVE-BOTTOM";
    NSWindow *window = nil;
    TerminalView *view = [self openWindowWithFrame:NSMakeRect(0, 0, 800, 480)
                                          clientID:@"scrolled-late"
                                          fontSize:14
                                          activate:YES
                                            window:&window];
    if (!view) return;

    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:8];
    [NSTimer scheduledTimerWithTimeInterval:0.05 repeats:YES block:^(NSTimer *timer) {
        if (view.ready && view.scrolledRows > 0) {
            NSString *viewport = [view readViewportText];
            if ([viewport containsString:expectHistory] && ![viewport containsString:forbidBottom]) {
                fprintf(stdout, "Verified late attach scrolled viewport: initial_scrolled=%llu current_scrolled=%llu\n",
                    view.initialScrolledRows, view.scrolledRows);
                [timer invalidate];
                [self finishWithStatus:0];
                return;
            }
        }
        if (deadline.timeIntervalSinceNow <= 0) {
            NSString *viewport = [view readViewportText];
            ghostty_surface_size_s sz = ghostty_surface_size(view.surface);
            fprintf(stderr, "Scrolled late attach check failed (ready=%d initial=%llu current=%llu size=%ux%u px=%ux%u cell=%ux%u). Viewport:\n%s\n",
                view.ready, view.initialScrolledRows, view.scrolledRows,
                sz.columns, sz.rows, sz.width_px, sz.height_px, sz.cell_width_px, sz.cell_height_px, viewport.UTF8String);
            [timer invalidate];
            [self finishWithStatus:1];
        }
    }];
}

- (void)runOwnershipAndReconnectTest {
    NSWindow *w1 = nil;
    TerminalView *v1 = [self openWindowWithFrame:NSMakeRect(0, 0, 760, 440)
                                        clientID:@"win-1"
                                        fontSize:0
                                        activate:YES
                                          window:&w1];
    if (!v1) return;

    __block NSWindow *w2 = nil;
    __block TerminalView *v2 = nil;
    __block NSWindow *w3 = nil;
    __block TerminalView *v3 = nil;
    __block int step = 0;
    __block NSDate *stepStart = [NSDate date];
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:12];

    [NSTimer scheduledTimerWithTimeInterval:0.05 repeats:YES block:^(NSTimer *timer) {
        if (deadline.timeIntervalSinceNow <= 0) {
            fprintf(stderr, "Ownership test timed out at step %d\n", step);
            [timer invalidate];
            [self finishWithStatus:1];
            return;
        }
        if (step == 0 && v1.ready && v1.inputOwner) {
            step = 1;
            v2 = [self openWindowWithFrame:NSMakeRect(40, 40, 760, 440)
                                  clientID:@"win-2"
                                  fontSize:0
                                  activate:NO
                                    window:&w2];
        } else if (step == 1 && v2.ready && !v2.inputOwner && [v2.ownerID isEqualToString:@"win-1"]) {
            // Non-owner v2 tries keyboard + mouse input; must NOT reach session.
            [v2 insertText:@"win2-blocked\n" replacementRange:NSMakeRange(NSNotFound, 0)];
            NSEvent *clickDown = [NSEvent mouseEventWithType:NSEventTypeLeftMouseDown
                location:NSMakePoint(40, 40) modifierFlags:0 timestamp:0
                windowNumber:w2.windowNumber context:nil eventNumber:0 clickCount:1 pressure:1.0];
            [v2 mouseDown:clickDown];
            NSEvent *clickUp = [NSEvent mouseEventWithType:NSEventTypeLeftMouseUp
                location:NSMakePoint(40, 40) modifierFlags:0 timestamp:0
                windowNumber:w2.windowNumber context:nil eventNumber:0 clickCount:1 pressure:0.0];
            [v2 mouseUp:clickUp];

            // Owner v1 sends keyboard input; must reach session and replicate to both v1 & v2.
            [v1 insertText:@"win1-ok\n" replacementRange:NSMakeRange(NSNotFound, 0)];
            step = 2;
            stepStart = [NSDate date];
        } else if (step == 2) {
            NSString *s1 = [v1 readScreenText];
            NSString *s2 = [v2 readScreenText];
            if ([s1 containsString:@"win2-blocked"] || [s2 containsString:@"win2-blocked"]) {
                fprintf(stderr, "Non-owner win-2 input unexpectedly reached session!\n");
                [timer invalidate];
                [self finishWithStatus:1];
                return;
            }
            if ([s1 containsString:@"win1-ok"] && [s2 containsString:@"win1-ok"] &&
                -[stepStart timeIntervalSinceNow] >= 0.15) {
                // Detach win-1, then claim input on win-2.
                [v1 detach];
                [w1 orderOut:nil];
                [self.views removeObject:v1];
                [self.windows removeObject:w1];
                step = 3;
            }
        } else if (step == 3 && v2.ownerID.length == 0) {
            [v2 claimInput];
            step = 4;
        } else if (step == 4 && v2.inputOwner) {
            [v2 insertText:@"win2-claimed\n" replacementRange:NSMakeRange(NSNotFound, 0)];
            NSEvent *clickDown = [NSEvent mouseEventWithType:NSEventTypeLeftMouseDown
                location:NSMakePoint(60, 100) modifierFlags:0 timestamp:0
                windowNumber:w2.windowNumber context:nil eventNumber:0 clickCount:1 pressure:1.0];
            [v2 mouseDown:clickDown];
            NSEvent *clickUp = [NSEvent mouseEventWithType:NSEventTypeLeftMouseUp
                location:NSMakePoint(60, 100) modifierFlags:0 timestamp:0
                windowNumber:w2.windowNumber context:nil eventNumber:0 clickCount:1 pressure:0.0];
            [v2 mouseUp:clickUp];
            step = 5;
        } else if (step == 5) {
            NSString *s2 = [v2 readScreenText];
            if ([s2 containsString:@"win2-claimed"] && [s2 containsString:@"mouse-seen"]) {
                // Detach win-2 and reconnect with win-3; win-3 must receive history and auto-own input.
                [v2 detach];
                [w2 orderOut:nil];
                [self.views removeObject:v2];
                [self.windows removeObject:w2];
                v3 = [self openWindowWithFrame:NSMakeRect(60, 60, 760, 440)
                                      clientID:@"win-3"
                                      fontSize:0
                                      activate:YES
                                        window:&w3];
                step = 6;
            }
        } else if (step == 6 && v3.ready && v3.inputOwner) {
            NSString *s3 = [v3 readScreenText];
            if ([s3 containsString:@"win1-ok"] && [s3 containsString:@"win2-claimed"]) {
                [v3 insertText:@"win3-reconnected\n" replacementRange:NSMakeRange(NSNotFound, 0)];
                step = 7;
            }
        } else if (step == 7) {
            NSString *s3 = [v3 readScreenText];
            if ([s3 containsString:@"win3-reconnected"]) {
                fprintf(stdout, "Verified ownership gating, claim_input transfer, and reconnect history\n");
                [timer invalidate];
                [self finishWithStatus:0];
            }
        }
    }];
}

- (void)runScrollBenchmark {
    double width = (NSProcessInfo.processInfo.environment[@"FLOWS_BENCH_WIDTH"] ?: @"1126").doubleValue;
    double height = (NSProcessInfo.processInfo.environment[@"FLOWS_BENCH_HEIGHT"] ?: @"688").doubleValue;
    float fontSize = (NSProcessInfo.processInfo.environment[@"FLOWS_BENCH_FONT_SIZE"] ?: @"19").floatValue;
    NSString *readyMarker = NSProcessInfo.processInfo.environment[@"FLOWS_BENCH_READY"] ?: @"ROW-9000";

    NSWindow *window = nil;
    TerminalView *view = [self openWindowWithFrame:NSMakeRect(0, 0, width, height)
                                          clientID:@"bench-native"
                                          fontSize:fontSize
                                          activate:YES
                                            window:&window];
    if (!view) return;

    NSDate *readyDeadline = [NSDate dateWithTimeIntervalSinceNow:12];
    __block BOOL started = NO;
    __block NSInteger ticksSent = 0;
    const NSInteger totalTicks = 240; // 60 Hz for 4.0 seconds

    [NSTimer scheduledTimerWithTimeInterval:0.05 repeats:YES block:^(NSTimer *waitTimer) {
        if (started) return;
        NSString *vp = [view readViewportText];
        if (view.ready && view.inputOwner && [vp containsString:readyMarker]) {
            started = YES;
            [waitTimer invalidate];

            NSString *startVp = [view readViewportText];
            [view startRenderMetrics];
            [NSTimer scheduledTimerWithTimeInterval:(1.0 / 60.0) repeats:YES block:^(NSTimer *scrollTimer) {
                // Positive scrollingDeltaY scrolls up into history by 3 lines per 60 Hz tick (~720 rows over 4s).
                ghostty_surface_mouse_scroll(view.surface, 0.0, 3.0, 0);
                ticksSent++;
                if (ticksSent >= totalTicks) {
                    [scrollTimer invalidate];
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(80 * NSEC_PER_MSEC)), dispatch_get_main_queue(), ^{
                        FlowsTerminalMetricsSummary *m = [view stopRenderMetrics];
                        NSString *endVp = [view readViewportText];
                        BOOL moved = (m.finalScrolledRows > m.initialScrolledRows) &&
                                     [startVp containsString:readyMarker] &&
                                     ![endVp containsString:readyMarker];
                        BOOL perfPass = moved && (m.submissionRateHz >= 55.0) && (m.submissionP95GapMs > 0.0) && (m.submissionP95GapMs < 33.4);
                        fprintf(stdout,
                            "BENCHMARK_RESULT duration_ms=%.1f scroll_events=%ld submissions=%lu "
                            "submission_rate_hz=%.2f p50_gap_ms=%.2f p95_gap_ms=%.2f max_gap_ms=%.2f "
                            "missed_33_4ms=%lu display_link_callbacks=%lu display_link_hz=%.2f "
                            "display_link_p95_ms=%.2f metal_layer=%d scrollbar_updates=%lu "
                            "distinct_offsets=%lu initial_scrolled=%llu final_scrolled=%llu history_moved=%d gate_pass=%d\n",
                            m.durationMs, (long)ticksSent, (unsigned long)m.renderSubmissions,
                            m.submissionRateHz, m.submissionP50GapMs, m.submissionP95GapMs, m.submissionMaxGapMs,
                            (unsigned long)m.missed33_4ms, (unsigned long)m.displayLinkCallbacks, m.displayLinkRateHz,
                            m.displayLinkP95GapMs, m.metalLayerAttached ? 1 : 0, (unsigned long)m.scrollbarUpdates,
                            (unsigned long)m.distinctScrollbarOffsets, m.initialScrolledRows, m.finalScrolledRows,
                            moved ? 1 : 0, perfPass ? 1 : 0);
                        [self finishWithStatus:(perfPass ? 0 : 1)];
                    });
                }
            }];
        } else if (readyDeadline.timeIntervalSinceNow <= 0) {
            fprintf(stderr, "Benchmark timed out waiting for %s (ready=%d owner=%d)\n",
                readyMarker.UTF8String, view.ready, view.inputOwner);
            [waitTimer invalidate];
            [self finishWithStatus:1];
        }
    }];
}

@end

int main(int argc, char **argv) {
    if (argc < 3) {
        fprintf(stderr, "usage: flows-terminal-window PROBE_EXE SOCKET [SESSION]\n");
        return 2;
    }
    @autoreleasepool {
        NSApplication *app = NSApplication.sharedApplication;
        [app setActivationPolicy:NSApplicationActivationPolicyRegular];
        ProbeApp *delegate = [ProbeApp new];
        delegate.probeExecutable = [NSString stringWithUTF8String:argv[1]];
        delegate.socketPath = [NSString stringWithUTF8String:argv[2]];
        delegate.sessionID = argc >= 4 ? [NSString stringWithUTF8String:argv[3]] : @"probe";
        app.delegate = delegate;
        [app run];
        [FlowsGhosttyRuntime.sharedRuntime shutdown];
    }
    return gRunStatus;
}
