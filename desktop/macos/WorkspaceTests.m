#import <AppKit/AppKit.h>
#include <assert.h>
#include <stdio.h>

// Compiled together with WorkspaceView.m in native desktop verification.
#import "WorkspaceView.m"

int main(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        FlowsWorkspaceController *ctrl = [[FlowsWorkspaceController alloc] init];

        // 1. Terminal-only controls hidden when no session exists.
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

        printf("WorkspaceTests passed\n");
    }
    return 0;
}
