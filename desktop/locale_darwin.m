//go:build darwin && cgo

#import <Foundation/Foundation.h>
#include <string.h>

char *linkportPreferredLanguage(void) {
    @autoreleasepool {
        NSString *language = [[NSLocale preferredLanguages] firstObject];
        return language ? strdup([language UTF8String]) : NULL;
    }
}
