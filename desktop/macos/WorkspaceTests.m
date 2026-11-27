#import <AppKit/AppKit.h>
#include <assert.h>
#include <stdio.h>

// Compiled together with WorkspaceView.m in native desktop verification.
#import "WorkspaceView.m"

int main(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        FlowsWorkspaceController *ctrl = [[FlowsWorkspaceController alloc] init];

        // 1. Terminal-only controls hidden when no session exists, and native TerminalView embedded in dock.
        assert(ctrl.terminalView != nil);
        assert(ctrl.terminalView.superview == ctrl.terminalDockView);
        assert(ctrl.terminalDockView.hidden == YES);
        assert(ctrl.detachButton.hidden == YES);
        assert(ctrl.dockOpen == NO);
        assert(ctrl.inputOwner == NO);

        // 2. Focus switching without terminal dock.
        assert(ctrl.window.firstResponder == ctrl.canvasView);
        [ctrl cycleFocus];
        assert(ctrl.window.firstResponder == ctrl.inspectorView);
        [ctrl cycleFocus];
        assert(ctrl.window.firstResponder == ctrl.sidebarView);

        // 3. Attach session opens dock, shows terminal controls, claims input ownership.
        [ctrl attachSession:@"sess-1"];
        assert(ctrl.dockOpen == YES);
        assert(ctrl.inputOwner == YES);
        assert(ctrl.terminalDockView.hidden == NO);
        assert(ctrl.detachButton.hidden == NO);
        assert(ctrl.window.firstResponder == ctrl.terminalDockView);

        // 4. Escape routes to harness when terminal has focus.
        NSEvent *esc = [NSEvent keyEventWithType:NSEventTypeKeyDown
                                        location:NSZeroPoint
                                   modifierFlags:0
                                       timestamp:0
                                    windowNumber:ctrl.window.windowNumber
                                         context:nil
                                      characters:@"\x1b"
                     charactersIgnoringModifiers:@"\x1b"
                                       isARepeat:NO
                                         keyCode:53];
        assert([ctrl handleKeyEvent:esc] == YES);
        assert(ctrl.dockOpen == YES);

        // 5. Cmd+W detaches dock and releases input ownership.
        NSEvent *cmdW = [NSEvent keyEventWithType:NSEventTypeKeyDown
                                         location:NSZeroPoint
                                    modifierFlags:NSEventModifierFlagCommand
                                        timestamp:0
                                     windowNumber:ctrl.window.windowNumber
                                          context:nil
                                       characters:@"w"
                      charactersIgnoringModifiers:@"w"
                                        isARepeat:NO
                                          keyCode:13];
        assert([ctrl handleKeyEvent:cmdW] == YES);
        assert(ctrl.dockOpen == NO);
        assert(ctrl.inputOwner == NO);
        assert(ctrl.terminalDockView.hidden == YES);

        // 6. Window close releases input ownership while preserving activeSessionID.
        [ctrl attachSession:@"sess-2"];
        assert(ctrl.inputOwner == YES);
        [ctrl windowWillClose:[NSNotification notificationWithName:NSWindowWillCloseNotification object:ctrl.window]];
        assert(ctrl.inputOwner == NO);
        assert([ctrl.activeSessionID isEqualToString:@"sess-2"]);

        // 7. Live native WorkspaceView attach, focus/input, scroll, resize, detach, and reconnect.
        NSString *probeExe = NSProcessInfo.processInfo.environment[@"FLOWS_WORKSPACE_PROBE_EXE"];
        NSString *sock = NSProcessInfo.processInfo.environment[@"FLOWS_WORKSPACE_SOCKET"];
        NSString *sessID = NSProcessInfo.processInfo.environment[@"FLOWS_WORKSPACE_SESSION"] ?: @"probe";
        if (probeExe.length > 0 && sock.length > 0) {
            [ctrl.window makeKeyAndOrderFront:nil];
            BOOL attached = [ctrl attachSession:sessID executable:probeExe socketPath:sock nonce:nil];
            assert(attached == YES);
            assert(ctrl.window.firstResponder == ctrl.terminalView);
            NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:8];
            while (!(ctrl.terminalView.ready && ctrl.terminalView.inputOwner) && deadline.timeIntervalSinceNow > 0) {
                [NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
            }
            assert(ctrl.terminalView.ready && ctrl.terminalView.inputOwner);
            [ctrl.terminalView insertText:@"ws-input-ok\n" replacementRange:NSMakeRange(NSNotFound, 0)];
            deadline = [NSDate dateWithTimeIntervalSinceNow:4];
            while (![[ctrl.terminalView readScreenText] containsString:@"ws-input-ok"] && deadline.timeIntervalSinceNow > 0) {
                [NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
            }
            assert([[ctrl.terminalView readScreenText] containsString:@"ws-input-ok"]);

            // Resize workspace window and verify terminal view resizes cleanly.
            [ctrl.window setContentSize:NSMakeSize(1040, 680)];
            [ctrl.terminalView syncSize];
            [ctrl.terminalView scrollLines:-3];
            [NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.15]];

            // Detach via Cmd+W and reconnect to verify history persists.
            assert([ctrl handleKeyEvent:cmdW] == YES);
            assert(ctrl.dockOpen == NO);
            assert(ctrl.terminalView.surface == NULL);

            BOOL reattached = [ctrl attachSession:sessID executable:probeExe socketPath:sock nonce:nil];
            assert(reattached == YES);
            deadline = [NSDate dateWithTimeIntervalSinceNow:8];
            while (!(ctrl.terminalView.ready && ctrl.terminalView.inputOwner &&
                     [[ctrl.terminalView readScreenText] containsString:@"ws-input-ok"]) &&
                   deadline.timeIntervalSinceNow > 0) {
                [NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
            }
            assert(ctrl.terminalView.ready && ctrl.terminalView.inputOwner);
            assert([[ctrl.terminalView readScreenText] containsString:@"ws-input-ok"]);
            [ctrl detachDock];
        }

        printf("WorkspaceTests passed\n");
    }
    return 0;
}
