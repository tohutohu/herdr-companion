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
