// Starter build for a GoLand plugin using the IntelliJ Platform Gradle Plugin 2.x.
// Versions are illustrative — bump to whatever's current when you run this.
plugins {
    id("org.jetbrains.kotlin.jvm") version "2.1.20"
    id("org.jetbrains.intellij.platform") version "2.16.0"
}

group = "dev.unfold"
version = "0.1.1"

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        // Compile against the target GoLand (currently 2025.3.1.1 / build 253)
        // so the Go PSI + embedded-editor APIs match the IDE it'll run in.
        goland("2025.3.1.1")
        bundledPlugin("org.jetbrains.plugins.go") // Go PSI
    }
}

intellijPlatform {
    pluginConfiguration {
        ideaVersion {
            // Built against 2025.3 (253). No untilBuild → open-ended, compatible
            // with all future IDE builds (a real number here would just lock out
            // newer GoLand releases; the Marketplace flags a fabricated one like
            // "299.*"). Re-introduce a bound if a future API break needs it.
            sinceBuild = "253"
            untilBuild = provider { null }
        }
    }

    // Marketplace requires every uploaded archive to be signed. Keys/passwords
    // come from the environment — never commit them. Generate the cert+key with
    // the `openssl` commands in scaffold-notes.md and export these before
    // `./gradlew signPlugin` / `publishPlugin`.
    signing {
        certificateChainFile = providers.environmentVariable("CERTIFICATE_CHAIN_FILE").map { file(it) }
        privateKeyFile = providers.environmentVariable("PRIVATE_KEY_FILE").map { file(it) }
        password = providers.environmentVariable("PRIVATE_KEY_PASSWORD")
    }

    // `publishPlugin` uploads to the JetBrains Marketplace. The token is a
    // permanent token from plugins.jetbrains.com → My Tokens.
    publishing {
        token = providers.environmentVariable("PUBLISH_TOKEN")
    }
}

kotlin {
    jvmToolchain(21)
}
