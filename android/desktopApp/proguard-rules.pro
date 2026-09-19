# Optional platform integrations referenced by OkHttp are not packaged by Herdr.
# Keep ProGuard strict for application classes while suppressing only these
# optional providers, which are absent from the Compose Desktop runtime.
-dontwarn com.oracle.svm.core.annotate.**
-dontwarn org.graalvm.nativeimage.hosted.**
-dontwarn org.bouncycastle.**
-dontwarn org.conscrypt.**
-dontwarn org.openjsse.**
