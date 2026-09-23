# Optional platform integrations referenced by OkHttp are not packaged by Herdr.
# Keep ProGuard strict for application classes while suppressing only these
# optional providers, which are absent from the Compose Desktop runtime.
# Compose Desktop's Kotlin-generated debug tables contain overlapping local
# variable entries. ProGuard optimization collapses them into duplicate entries
# that the bundled JBR rejects with ClassFormatError at startup.
-dontoptimize
-dontwarn com.oracle.svm.core.annotate.**
-dontwarn org.graalvm.nativeimage.hosted.**
-dontwarn org.bouncycastle.**
-dontwarn org.conscrypt.**
-dontwarn org.openjsse.**

# Coil finds its network fetcher through META-INF/services. ProGuard keeps the
# service file but strips the provider it names, and the ServiceLoader error
# then fails every image request, local files included.
-keep class * implements coil3.util.FetcherServiceLoaderTarget { <init>(); }
-keep class * implements coil3.util.DecoderServiceLoaderTarget { <init>(); }
