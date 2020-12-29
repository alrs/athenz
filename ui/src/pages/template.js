/*
 * Copyright 2020 Verizon Media
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
import React from 'react';
import Header from '../components/header/Header';
import UserDomains from '../components/domain/UserDomains';
import API from '../api';
import styled from '@emotion/styled';
import Head from 'next/head';
// there is an issue with next-link and next-css if the css is not present then it doesnt load so adding this
import 'flatpickr/dist/themes/light.css';
import DomainDetails from '../components/header/DomainDetails';
import TemplateList from '../components/template/TemplateList';
import RequestUtils from '../components/utils/RequestUtils';
import Tabs from '../components/header/Tabs';
import Error from './_error';
import createCache from '@emotion/cache';
import { CacheProvider } from '@emotion/react';

const AppContainerDiv = styled.div`
    align-items: stretch;
    flex-flow: row nowrap;
    height: 100%;
    display: flex;
    justify-content: flex-start;
`;

const MainContentDiv = styled.div`
    flex: 1 1 calc(100vh - 60px);
    overflow: hidden;
    font: 300 14px HelveticaNeue-Reg, Helvetica, Arial, sans-serif;
`;

const ServicesContainerDiv = styled.div`
    align-items: stretch;
    flex: 1 1;
    height: calc(100vh - 60px);
    overflow: auto;
    display: flex;
    flex-direction: column;
`;

const ServicesContentDiv = styled.div``;

const PageHeaderDiv = styled.div`
    background: linear-gradient(to top, #f2f2f2, #fff);
    padding: 20px 30px 0;
`;

const TitleDiv = styled.div`
    font: 600 20px HelveticaNeue-Reg, Helvetica, Arial, sans-serif;
    margin-bottom: 10px;
`;

export default class TemplatePage extends React.Component {
    static async getInitialProps(props) {
        let api = API(props.req);
        let reload = false;
        let notFound = false;
        let error = undefined;
        const domains = await Promise.all([
            api.listUserDomains(),
            api.getHeaderDetails(),
            api.getDomain(props.query.domain),
            api.getServices(props.query.domain),
            api.getForm(),
            api.getPendingDomainRoleMembersList(),
            api.getServicePageConfig(),
            api.isAWSTemplateApplied(props.query.domain),
            api.getDomainTemplateDetailsList(props.query.domain),
        ]).catch((err) => {
            let response = RequestUtils.errorCheckHelper(err);
            reload = response.reload;
            error = response.error;
            return [{}, {}, {}, {}, {}, {}, {}, {}, {}];
        });
        let domainDetails = domains[2];
        domainDetails.isAWSTemplateApplied = !!domains[7];
        return {
            api,
            reload,
            notFound,
            error,
            domains: domains[0],
            headerDetails: domains[1],
            domain: props.query.domain,
            domainDetails,
            services: domains[3],
            _csrf: domains[4],
            pending: domains[5],
            pageConfig: domains[6],
            domainTemplateDetails: domains[8],
            nonce: props.req.headers.rid,
        };
    }

    constructor(props) {
        super(props);
        this.api = props.api || API();
        this.cache = createCache({
            key: 'athenz',
            nonce: this.props.nonce,
        });
    }

    render() {
        const {
            domain,
            reload,
            domainDetails,
            domainTemplateDetails,
            services,
            _csrf,
        } = this.props;
        if (reload) {
            window.location.reload();
            return <div />;
        }
        if (this.props.error) {
            return <Error err={this.props.error} />;
        }
        return (
            <CacheProvider value={this.cache}>
                <div data-testid='template'>
                    <Head>
                        <title>Athenz for Template</title>
                    </Head>
                    <Header
                        showSearch={true}
                        headerDetails={this.props.headerDetails}
                        pending={this.props.pending}
                    />
                    <MainContentDiv>
                        <AppContainerDiv>
                            <ServicesContainerDiv>
                                <ServicesContentDiv>
                                    <PageHeaderDiv>
                                        <TitleDiv>{domain}</TitleDiv>
                                        <DomainDetails
                                            domainDetails={domainDetails}
                                            api={this.api}
                                            _csrf={_csrf}
                                            productMasterLink={
                                                this.props.headerDetails
                                                    .productMasterLink
                                            }
                                        />
                                        <Tabs
                                            api={this.api}
                                            domain={domain}
                                            selectedName={'templates'}
                                        />
                                    </PageHeaderDiv>
                                    <TemplateList
                                        api={this.api}
                                        domain={domain}
                                        domainTemplateDetails={
                                            this.props.domainTemplateDetails
                                        }
                                        _csrf={this.props._csrf}
                                        pageConfig={this.props.pageConfig}
                                    />
                                </ServicesContentDiv>
                            </ServicesContainerDiv>
                            <UserDomains
                                domains={this.props.domains}
                                api={this.api}
                                domain={domain}
                            />
                        </AppContainerDiv>
                    </MainContentDiv>
                </div>
            </CacheProvider>
        );
    }
}
