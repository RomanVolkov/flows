import CoreText
import Foundation

for file in CommandLine.arguments.dropFirst() {
    let url = URL(fileURLWithPath: file)
    guard let descriptors = CTFontManagerCreateFontDescriptorsFromURL(url as CFURL) as? [CTFontDescriptor],
          descriptors.count == 1,
          let family = CTFontDescriptorCopyAttribute(descriptors[0], kCTFontFamilyNameAttribute) as? String,
          family == "Iosevka" else {
        FileHandle.standardError.write(Data("Unexpected font family: \(file)\n".utf8))
        exit(1)
    }
}
