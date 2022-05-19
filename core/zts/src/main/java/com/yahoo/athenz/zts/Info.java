//
// This file generated by rdl 1.5.2. Do not modify!
//

package com.yahoo.athenz.zts;
import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonInclude;
import com.yahoo.rdl.*;

//
// Info - Copyright Athenz Authors Licensed under the terms of the Apache
// version 2.0 license. See LICENSE file for terms. The representation for an
// info object
//
@JsonIgnoreProperties(ignoreUnknown = true)
public class Info {
    @RdlOptional
    @JsonInclude(JsonInclude.Include.NON_EMPTY)
    public String buildJdkSpec;
    @RdlOptional
    @JsonInclude(JsonInclude.Include.NON_EMPTY)
    public String implementationTitle;
    @RdlOptional
    @JsonInclude(JsonInclude.Include.NON_EMPTY)
    public String implementationVersion;
    @RdlOptional
    @JsonInclude(JsonInclude.Include.NON_EMPTY)
    public String implementationVendor;

    public Info setBuildJdkSpec(String buildJdkSpec) {
        this.buildJdkSpec = buildJdkSpec;
        return this;
    }
    public String getBuildJdkSpec() {
        return buildJdkSpec;
    }
    public Info setImplementationTitle(String implementationTitle) {
        this.implementationTitle = implementationTitle;
        return this;
    }
    public String getImplementationTitle() {
        return implementationTitle;
    }
    public Info setImplementationVersion(String implementationVersion) {
        this.implementationVersion = implementationVersion;
        return this;
    }
    public String getImplementationVersion() {
        return implementationVersion;
    }
    public Info setImplementationVendor(String implementationVendor) {
        this.implementationVendor = implementationVendor;
        return this;
    }
    public String getImplementationVendor() {
        return implementationVendor;
    }

    @Override
    public boolean equals(Object another) {
        if (this != another) {
            if (another == null || another.getClass() != Info.class) {
                return false;
            }
            Info a = (Info) another;
            if (buildJdkSpec == null ? a.buildJdkSpec != null : !buildJdkSpec.equals(a.buildJdkSpec)) {
                return false;
            }
            if (implementationTitle == null ? a.implementationTitle != null : !implementationTitle.equals(a.implementationTitle)) {
                return false;
            }
            if (implementationVersion == null ? a.implementationVersion != null : !implementationVersion.equals(a.implementationVersion)) {
                return false;
            }
            if (implementationVendor == null ? a.implementationVendor != null : !implementationVendor.equals(a.implementationVendor)) {
                return false;
            }
        }
        return true;
    }
}
