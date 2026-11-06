#import <Cocoa/Cocoa.h>
#include "_cgo_export.h"

// Нативный пункт в меню-баре macOS: показывает текущий ворктри и меню со
// списком всех ворктри. Клик по пункту вызывает goMenubarClicked(tag).

static NSStatusItem *statusItem = nil;

@interface WTMenuTarget : NSObject
@end

@implementation WTMenuTarget
- (void)clicked:(NSMenuItem *)sender {
	goMenubarClicked((int)sender.tag);
}
@end

static WTMenuTarget *menuTarget = nil;

void statusbarStart(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (statusItem) return;
		menuTarget = [[WTMenuTarget alloc] init];
		statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
		statusItem.button.title = @"Worktrees";
		NSMenu *menu = [[NSMenu alloc] init];
		menu.autoenablesItems = NO;
		statusItem.menu = menu;
	});
}

void statusbarSetTitle(const char *s) {
	NSString *str = [NSString stringWithUTF8String:s];
	dispatch_async(dispatch_get_main_queue(), ^{
		if (statusItem) statusItem.button.title = str;
	});
}

void statusbarClearMenu(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (statusItem) [statusItem.menu removeAllItems];
	});
}

void statusbarAddSeparator(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (statusItem) [statusItem.menu addItem:[NSMenuItem separatorItem]];
	});
}

void statusbarAddItem(const char *label, int tag, int checked, int enabled) {
	NSString *l = [NSString stringWithUTF8String:label];
	dispatch_async(dispatch_get_main_queue(), ^{
		if (!statusItem) return;
		NSMenuItem *it = [[NSMenuItem alloc] initWithTitle:l action:@selector(clicked:) keyEquivalent:@""];
		it.target = menuTarget;
		it.tag = tag;
		it.state = checked ? NSControlStateValueOn : NSControlStateValueOff;
		it.enabled = enabled ? YES : NO;
		[statusItem.menu addItem:it];
	});
}
