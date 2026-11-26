#import <AppKit/AppKit.h>
#import "WorkspaceView.m"

@interface FlowsAppDelegate : NSObject <NSApplicationDelegate>
@property(nonatomic, strong) FlowsWorkspaceController *workspace;
@end

@implementation FlowsAppDelegate

- (void)buildMainMenu {
    NSMenu *menubar = [[NSMenu alloc] init];
    NSMenuItem *appMenuItem = [[NSMenuItem alloc] init];
    [menubar addItem:appMenuItem];
    [NSApp setMainMenu:menubar];

    NSMenu *appMenu = [[NSMenu alloc] init];
    NSMenuItem *quitItem = [[NSMenuItem alloc] initWithTitle:@"Quit Flows"
                                                      action:@selector(terminate:)
                                               keyEquivalent:@"q"];
    [appMenu addItem:quitItem];
    [appMenuItem setSubmenu:appMenu];
}

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    (void)notification;
    [self buildMainMenu];
    self.workspace = [[FlowsWorkspaceController alloc] init];
    [self.workspace.window center];
    [self.workspace.window makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
}

- (void)application:(NSApplication *)application openURLs:(NSArray<NSURL *> *)urls {
    (void)application;
    if (urls.count == 0) return;
    NSURL *url = urls.firstObject;
    if (!self.workspace) {
        self.workspace = [[FlowsWorkspaceController alloc] init];
        [self.workspace.window makeKeyAndOrderFront:nil];
    }
    self.workspace.selectionSummaryLabel.stringValue =
        [NSString stringWithFormat:@"Import preview: %@", url.lastPathComponent ?: url.path];
}

- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)sender {
    (void)sender;
    return YES;
}

@end

int main(int argc, const char *argv[]) {
    (void)argc;
    (void)argv;
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyRegular];
        FlowsAppDelegate *delegate = [[FlowsAppDelegate alloc] init];
        [app setDelegate:delegate];
        [app run];
    }
    return 0;
}
