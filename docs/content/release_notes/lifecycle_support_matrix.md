# Lifecycle and support matrix {#hak_lifecycle .reference}

The following table details the IBM block storage CSI driver lifecycle with compatible IBM storage system microcodes and supported orchestration platforms.

**Note:**

-   The IBM® Storage Virtualize family includes Spectrum Virtualize as software only, Storage Virtualize for Public Cloud, SAN Volume Controller \(SVC\), Storwize and FlashSystem® family members built with Spectrum® Virtualize \(including FlashSystem 5xxx, 7xxx, 9xxx\).
-   IBM Storage Virtualize family storage systems run the IBM Spectrum Virtualize™ software. In addition, the IBM Spectrum Virtualize package is available as a deployable solution that can be run on any compatible hardware.
-   The IBM Storage Virtualize family microcode versions 8.4.x, 8.5.x, 8.6.x and 8.7.x include both LTS and Non-LTS releases. For more information, see [IBM Spectrum Virtualize FAQ for Continuous Development \(CD\) Release Model for software releases](https://www.ibm.com/support/pages/node/6409554).
-   Ubuntu 20.04.x is only supported from Kubernetes 1.23.
-   Ubuntu 22.04.x and 24.04.x are only supported from Kubernetes 1.29.
-   Virtualized worker nodes \(for example, VMware vSphere\) are supported with iSCSI and Fibre Channel \(FC\) adapters, when the FC adapter is used in passthrough mode.

|IBM Block Storage CSI driver version|General availability|End of support|Supported FlashSystem A9000 and A9000R microcode versions|Supported Storage Virtualize family microcode versions|Supported DS8000 family versions|Supported operating systems|Supported Kubernetes versions, platform architecture and installation methods|Supported Red Hat OpenShift versions, platform architecture and installation methods|
|------------------------------------|--------------------|--------------|---------------------------------------------------------|------------------------------------------------------|--------------------------------|---------------------------|-----------------------------------------------------------------------------|------------------------------------------------------------------------------------|
|1.12.0|December 2024|No|Not supported|8.4.x, 8.5.x, 8.6.x, 8.7.x|Not supported|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 8.x**   
 **Red Hat Enterprise Linux \(RHEL\) 9.x**   
 **Ubuntu 20.04.x LTS**  
 **Ubuntu 22.04.x LTS**  
 **Ubuntu 24.04.x LTS**  
   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.14, 4.15, 4.16, 4.17**

|1.29, 1.30, 1.31 -   x86
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.12.0?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.12.0?topic=driver-installing-operatorhubio)

|4.14, 4.15, 4.16, 4.17-   x86
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.12.0?topic=driver-installing-github)

|
|1.11.4|September 2024|15 June 2025|Not supported|8.4.x, 8.5.x, 8.6.x, 8.7.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 8.x**   
 **Red Hat Enterprise Linux \(RHEL\) 9.x**   
 **Ubuntu 20.04.x LTS**  
 **Ubuntu 22.04.x LTS**  
 **Ubuntu 24.04.x LTS**

   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.13, 4.14, 4.15, 4.16, 4.17**   


|1.27, 1.28, 1.29 -   x86
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.4?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.4?topic=driver-installing-operatorhubio)

|4.13, 4.14, 4.15, 4.16, 4.17-   x86
    -   [OpenShift Web Console](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.4?topic=driver-installing-openshift-web-console)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.4?topic=driver-installing-github)
-   IBM Z, IBM Power
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.4?topic=driver-installing-github)

|
|1.11.3|May 2024|17 January 2025|Not supported|8.4.x, 8.5.x, 8.6.x, 8.7.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 8.x**   
 **Red Hat Enterprise Linux \(RHEL\) 9.x**   
 **Ubuntu 20.04.x LTS**  
 **Ubuntu 22.04.x LTS**  
   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.12, 4.13, 4.14, 4.15**   


|1.27, 1.28, 1.29-   x86
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.3?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.3?topic=driver-installing-operatorhubio)

|4.12, 4.13, 4.14, 4.15-   x86
    -   [OpenShift Web Console](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.3?topic=driver-installing-openshift-web-console)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.3?topic=driver-installing-github)
-   IBM Z, IBM Power
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.3?topic=driver-installing-github)

|
|1.11.1|May 2023|8 November 2024|Not supported|7.8.x, 8.2.x, 8.3.x, 8.4.x, 8.5.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**   
 **Red Hat Enterprise Linux \(RHEL\) 8.x**   
 **Ubuntu 20.04.x LTS**  


   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.10, 4.11, 4.12, 4.13**   


|1.24, 1.25, 1.26, 1.27

 -   x86
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.1?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.1?topic=driver-installing-operatorhubio)
-   IBM Z \(RHEL 7.x only\)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.1?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.1?topic=driver-installing-operatorhubio)

|4.10, 4.11, 4.12, 4.13-   x86
    -   [OpenShift Web Console](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.1?topic=driver-installing-openshift-web-console)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.1?topic=driver-installing-github)
-   IBM Z, IBM Power
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.1?topic=driver-installing-github)

|
|1.11.0|January 2023|17 July 2024|Not supported|7.8.x, 8.2.x, 8.3.x, 8.4.x, 8.5.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**   
 **Red Hat Enterprise Linux \(RHEL\) 8.x**   
 **Ubuntu 20.04.x LTS**

   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.10, 4.11, 4.12**  


|1.24, 1.25, 1.26

 -   x86
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.0?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.0?topic=driver-installing-operatorhubio)
-   IBM Z \(RHEL 7.x only\)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.0?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.0?topic=driver-installing-operatorhubio)

|4.10, 4.11, 4.12-   x86
    -   [OpenShift Web Console](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.0?topic=driver-installing-openshift-web-console)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.0?topic=driver-installing-github)
-   IBM Z, IBM Power
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.11.0?topic=driver-installing-github)

|
|1.10.0|July 2022|10 February 2024|12.3.2.c or later|7.8.x, 8.2.x, 8.3.x, 8.4.x, 8.5.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**   
 **Red Hat Enterprise Linux \(RHEL\) 8.x**   
 **Ubuntu 20.04.x LTS**

   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.9, 4.10, 4.11**  


|1.23, 1.24

 -   x86
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.10.0?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.10.0?topic=driver-installing-operatorhubio)
-   IBM Z \(RHEL 7.x only\)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.10.0?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.10.0?topic=driver-installing-operatorhubio)

|4.9, 4.10, 4.11-   x86
    -   [OpenShift Web Console](https://www.ibm.com/docs/en/stg-block-csi-driver/1.10.0?topic=driver-installing-openshift-web-console)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.10.0?topic=driver-installing-github)
-   IBM Z, IBM Power
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.10.0?topic=driver-installing-github)

|
|1.9.0|March 2022|10 September 2023|12.3.2.c or later|7.8.x, 8.2.x, 8.3.x, 8.4.x, 8.5.x|8.x and higher with same API interface|  Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**   
 **Red Hat Enterprise Linux \(RHEL\) 8.x**   
 **Ubuntu 20.04.x LTS**

   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.8, 4.9, 4.10**  


|1.22, 1.23

 -   x86
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.9.0?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.9.0?topic=driver-installing-operatorhubio)
-   IBM Z \(RHEL 7.x only\)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.9.0?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.9.0?topic=driver-installing-operatorhubio)

|4.8, 4.9 , 4.10-   x86
    -   [OpenShift Web Console](https://www.ibm.com/docs/en/stg-block-csi-driver/1.9.0?topic=driver-installing-openshift-web-console)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.9.0?topic=driver-installing-github)
-   IBM Z, IBM Power
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.9.0?topic=driver-installing-github)

|
|1.8.0|December 2021|1 April 2023|12.3.2.c or later|7.8.x, 8.2.x, 8.3.x, 8.4.x, 8.5.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**   
   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.7, 4.8, 4.9**

|1.21, 1.22

 -   x86
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.8.0?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.8.0?topic=driver-installing-operatorhubio)
-   IBM Z
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.8.0?topic=driver-installing-github)
    -   [OperatorHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.8.0?topic=driver-installing-operatorhubio)

|4.7, 4.8, 4.9-   x86
    -   [OpenShift Web Console](https://www.ibm.com/docs/en/stg-block-csi-driver/1.8.0?topic=driver-installing-openshift-web-console)
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.8.0?topic=driver-installing-github)
-   IBM Z, IBM Power
    -   [GitHub](https://www.ibm.com/docs/en/stg-block-csi-driver/1.8.0?topic=driver-installing-github)

|
|1.7.0|September 2021|1 January 2023|12.3.2.c or later|7.8.x, 8.2.x, 8.3.x, 8.4.x, 8.5.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**

   


 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.8**

|1.21, 1.22

 -   x86
    -   GitHub
    -   OperatorHub
-   IBM Z
    -   GitHub
    -   OperatorHub

|4.8-   x86
    -   OpenShift Web Console
    -   GitHub
-   IBM Z, IBM Power
    -   GitHub

|
|1.6.0|June 2021|1 January 2023|12.3.2.c or later|7.8.x, 8.2.x, 8.3.x, 8.4.x, 8.5.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**

   


 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.7, 4.8**

|1.20, 1.21

 -   x86
    -   GitHub
    -   OperatorHub
-   IBM Z
    -   GitHub
    -   OperatorHub

|  
 4.7, 4.8

 -   x86
    -   OpenShift Web Console
    -   GitHub
-   IBM Z, IBM Power
    -   GitHub

|
|1.5.1|July 2021|24 August 2022|12.3.2.c or later|7.8.x, 8.2.x, 8.3.x, 8.4.x, 8.5.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**   
   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.6, 4.7**

|1.19, 1.20

 -   x86
    -   GitHub
    -   OperatorHub

|  
 4.6, 4.7

 -   x86
    -   OpenShift Web Console
    -   GitHub
-   IBM Z, IBM Power
    -   GitHub

|
|1.5.0|March 2021|24 August 2022|12.3.2.c or later|7.8.x, 8.2.x, 8.3.x, 8.4.x, 8.5.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**   
   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.6, 4.7**

|1.19, 1.20

 -   x86
    -   GitHub
    -   OperatorHub

|  
 4.6, 4.7

 -   x86
    -   OpenShift Web Console
    -   GitHub
-   IBM Z, IBM Power
    -   GitHub

|
|1.4.0|December 2020|18 October 2021|12.3.2.b or later|7.8.x, 8.2.x, 8.3.x, 8.4.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**   
   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.4, 4.5, 4.6**

|1.18, 1.19

 -   x86
    -   GitHub
    -   OperatorHub

|  
 4.4, 4.5, 4.6

 -   x86
    -   OpenShift Web Console
    -   GitHub
-   IBM Z, IBM Power
    -   GitHub

|
|1.3.0|September 2020|27 July 2021|12.3.2.b or later|7.8.x, 8.2.x, 8.3.x, 8.4.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**

   


 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.3, 4.4, 4.5**

|1.17, 1.18

 -   x86
    -   GitHub
    -   OperatorHub

|  
 4.3, 4.4, 4.5

 -   x86
    -   OpenShift Web Console
    -   GitHub
-   IBM Z, IBM Power
    -   GitHub

|
|1.2.0|June 2020|24 February 2021|12.3.2.b or later|7.8.x, 8.2.x, 8.3.x, 8.4.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**

   


 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.3, 4.4**

|1.16, 1.17

 -   x86
    -   GitHub

|4.3, 4.4-   x86
    -   OpenShift Web Console
    -   GitHub
-   IBM Z, IBM Power
    -   GitHub

|
|1.1.0|March 2020|27 October 2020|12.3.2.b or later|7.8.x, 8.2.x, 8.3.x, 8.4.x|8.x and higher with same API interface|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**

   
 Red Hat OpenShift support:  
 **Red Hat Enterprise Linux CoreOS \(RHCOS \) 4.2, 4.3**

|1.14, 1.16

 -   x86
    -   GitHub

|4.2, 4.3-   x86
    -   OpenShift Web Console
    -   GitHub
-   IBM Z
    -   GitHub

|
|1.0.0|November 2019|13 July 2020|12.3.2.b or later|**IBM FlashSystem V9000, IBM SAN Volume Controller \(SVC\), IBM Spectrum Virtualize as software only, IBM Storwize® V5000, IBM Storwize V7000:** 7.8.x, 8.2.x, 8.3.x, 8.4.x  
 **IBM FlashSystem 9100:** 8.2.1, 8.3.x, 8.4.x

|N/A|Kubernetes support:  
 **Red Hat Enterprise Linux \(RHEL\) 7.x**

|1.14

 -   x86
    -   GitHub

|N/A|
| |

